-- RockyCoin ledger for Supabase.
-- Run this whole file once in: Supabase dashboard -> SQL Editor -> New query -> Run.
-- It is safe to run again; it will not erase mined coins.

create extension if not exists pgcrypto with schema extensions;

-- Single-row chain state: latest coin hash, height, and mining rules.
create table if not exists public.chain_state (
  id int primary key check (id = 1),
  tip_hash text not null check (tip_hash ~ '^[a-f0-9]{64}$'),
  height bigint not null default 0,
  difficulty int not null default 6 check (difficulty between 1 and 8),
  min_seconds_between_coins int not null default 10 check (min_seconds_between_coins >= 0)
);

insert into public.chain_state (id, tip_hash)
values (1, repeat('0', 64))
on conflict (id) do nothing;

create table if not exists public.wallets (
  address text primary key check (address ~ '^RC[a-f0-9]{40}$'),
  created_at timestamptz not null default now()
);

-- One row per mined coin. Each coin's hash covers the previous coin's hash, so the rows form a chain.
create table if not exists public.mined_coins (
  id bigint generated always as identity primary key,
  height bigint not null unique,
  wallet text not null references public.wallets (address),
  nonce text not null check (nonce ~ '^[a-f0-9]{16}$'),
  prev_hash text not null,
  hash text not null unique check (hash ~ '^[a-f0-9]{64}$'),
  reward int not null check (reward between 1 and 10),
  created_at timestamptz not null default now()
);

create index if not exists mined_coins_wallet_idx on public.mined_coins (wallet, created_at desc);

-- Optional display name for a wallet (lowercase letters, digits, underscore).
alter table public.wallets add column if not exists name text check (name ~ '^[a-z0-9_]{3,20}$');
create unique index if not exists wallets_name_idx on public.wallets (name);

-- Transfers between wallets. Written only by the transfer() function below.
create table if not exists public.transfers (
  id bigint generated always as identity primary key,
  from_wallet text not null references public.wallets (address),
  to_wallet text not null references public.wallets (address),
  amount int not null check (amount > 0),
  created_at timestamptz not null default now(),
  check (from_wallet <> to_wallet)
);
create index if not exists transfers_from_idx on public.transfers (from_wallet, created_at desc);
create index if not exists transfers_to_idx on public.transfers (to_wallet, created_at desc);

-- balance = mined + received - sent
drop view if exists public.balances;
create view public.balances
with (security_invoker = on) as
select w.address as wallet,
       w.name,
       (coalesce((select sum(reward) from public.mined_coins m where m.wallet = w.address), 0)
        + coalesce((select sum(amount) from public.transfers t where t.to_wallet = w.address), 0)
        - coalesce((select sum(amount) from public.transfers t where t.from_wallet = w.address), 0))::bigint as balance,
       (select count(*) from public.mined_coins m where m.wallet = w.address)::bigint as coins
from public.wallets w;

-- The website can read everything but cannot write to any table directly.
alter table public.chain_state enable row level security;
alter table public.wallets enable row level security;
alter table public.mined_coins enable row level security;
alter table public.transfers enable row level security;

drop policy if exists "public read" on public.chain_state;
drop policy if exists "public read" on public.wallets;
drop policy if exists "public read" on public.mined_coins;
drop policy if exists "public read" on public.transfers;
create policy "public read" on public.chain_state for select to anon, authenticated using (true);
create policy "public read" on public.wallets for select to anon, authenticated using (true);
create policy "public read" on public.mined_coins for select to anon, authenticated using (true);
create policy "public read" on public.transfers for select to anon, authenticated using (true);

revoke all on public.chain_state, public.wallets, public.mined_coins, public.transfers, public.balances from anon, authenticated;
grant select on public.chain_state, public.wallets, public.mined_coins, public.transfers, public.balances to anon, authenticated;

-- The only way to write: submit a proof of work. The server recomputes the hash, so a client cannot fake a coin.
-- Hash input is: wallet || ':' || previous coin hash || ':' || nonce, hashed with SHA-256.
create or replace function public.submit_coin(p_wallet text, p_nonce text)
returns json
language plpgsql
security definer
set search_path = public, extensions
as $$
declare
  st public.chain_state%rowtype;
  v_hash text;
  v_reward int;
begin
  if p_wallet is null or p_wallet !~ '^RC[a-f0-9]{40}$' then
    raise exception 'invalid wallet address';
  end if;
  if p_nonce is null or p_nonce !~ '^[a-f0-9]{16}$' then
    raise exception 'invalid nonce';
  end if;

  -- Cheap check first, without a lock, so bad submissions cannot stall other miners.
  select * into st from public.chain_state where id = 1;
  v_hash := encode(digest(p_wallet || ':' || st.tip_hash || ':' || p_nonce, 'sha256'), 'hex');
  if left(v_hash, st.difficulty) <> repeat('0', st.difficulty) then
    raise exception 'invalid or stale proof of work';
  end if;

  -- Lock the chain tip and check again in case another coin was accepted in between.
  select * into st from public.chain_state where id = 1 for update;
  v_hash := encode(digest(p_wallet || ':' || st.tip_hash || ':' || p_nonce, 'sha256'), 'hex');
  if left(v_hash, st.difficulty) <> repeat('0', st.difficulty) then
    raise exception 'invalid or stale proof of work';
  end if;

  if exists (
    select 1 from public.mined_coins
    where wallet = p_wallet
      and created_at > now() - make_interval(secs => st.min_seconds_between_coins)
  ) then
    raise exception 'slow down: one coin every % seconds per wallet', st.min_seconds_between_coins;
  end if;

  -- The reward is random and chosen by the server, so miners cannot pick lucky hashes.
  v_reward := 1 + floor(random() * 10)::int;

  insert into public.wallets (address) values (p_wallet) on conflict do nothing;
  insert into public.mined_coins (height, wallet, nonce, prev_hash, hash, reward)
  values (st.height + 1, p_wallet, p_nonce, st.tip_hash, v_hash, v_reward);
  update public.chain_state set tip_hash = v_hash, height = st.height + 1 where id = 1;

  return json_build_object('hash', v_hash, 'height', st.height + 1, 'reward', v_reward);
end;
$$;

revoke all on function public.submit_coin(text, text) from public;
grant execute on function public.submit_coin(text, text) to anon, authenticated;

-- Wallet ownership: a wallet is created from a 64-hex secret key, and its address is derived from that key:
--   address = 'RC' || first 40 hex of SHA-256('rocky-address:' || secret)
-- Only someone who knows the secret can name the wallet or send coins from it.
create or replace function public.wallet_address(p_secret text)
returns text
language plpgsql
immutable
set search_path = public, extensions
as $$
begin
  if p_secret is null or p_secret !~ '^[a-f0-9]{64}$' then
    raise exception 'invalid secret key';
  end if;
  return 'RC' || left(encode(digest('rocky-address:' || p_secret, 'sha256'), 'hex'), 40);
end;
$$;

create or replace function public.register_wallet(p_secret text)
returns json
language plpgsql
security definer
set search_path = public, extensions
as $$
declare
  v_address text := public.wallet_address(p_secret);
  v_name text;
begin
  insert into public.wallets (address) values (v_address) on conflict do nothing;
  select name into v_name from public.wallets where address = v_address;
  return json_build_object('address', v_address, 'name', v_name);
end;
$$;

create or replace function public.set_wallet_name(p_secret text, p_name text)
returns json
language plpgsql
security definer
set search_path = public, extensions
as $$
declare
  v_address text := public.wallet_address(p_secret);
  v_name text := lower(trim(coalesce(p_name, '')));
begin
  if v_name !~ '^[a-z0-9_]{3,20}$' then
    raise exception 'name must be 3-20 characters: letters, numbers or underscore';
  end if;
  insert into public.wallets (address) values (v_address) on conflict do nothing;
  begin
    update public.wallets set name = v_name where address = v_address;
  exception when unique_violation then
    raise exception 'that name is already taken';
  end;
  return json_build_object('address', v_address, 'name', v_name);
end;
$$;

-- Send coins to an address (RC...) or a wallet name. The recipient must already exist on the ledger.
create or replace function public.transfer(p_secret text, p_to text, p_amount int)
returns json
language plpgsql
security definer
set search_path = public, extensions
as $$
declare
  v_from text := public.wallet_address(p_secret);
  v_to text;
  v_target text := lower(trim(coalesce(p_to, '')));
  v_balance bigint;
begin
  if p_amount is null or p_amount < 1 then
    raise exception 'amount must be at least 1';
  end if;

  if v_target ~ '^rc[a-f0-9]{40}$' then
    select address into v_to from public.wallets where address = upper(left(v_target, 2)) || substr(v_target, 3);
  else
    select address into v_to from public.wallets where name = ltrim(v_target, '@');
  end if;
  if v_to is null then
    raise exception 'recipient not found';
  end if;
  if v_to = v_from then
    raise exception 'you cannot send coins to yourself';
  end if;

  -- Serialise transfers so two requests cannot spend the same coins.
  perform 1 from public.chain_state where id = 1 for update;

  select balance into v_balance from public.balances where wallet = v_from;
  if coalesce(v_balance, 0) < p_amount then
    raise exception 'insufficient balance';
  end if;

  insert into public.transfers (from_wallet, to_wallet, amount) values (v_from, v_to, p_amount);
  return json_build_object('to', v_to, 'amount', p_amount, 'balance', v_balance - p_amount);
end;
$$;

revoke all on function public.wallet_address(text) from public;
revoke all on function public.register_wallet(text) from public;
revoke all on function public.set_wallet_name(text, text) from public;
revoke all on function public.transfer(text, text, int) from public;
grant execute on function public.register_wallet(text) to anon, authenticated;
grant execute on function public.set_wallet_name(text, text) to anon, authenticated;
grant execute on function public.transfer(text, text, int) to anon, authenticated;
