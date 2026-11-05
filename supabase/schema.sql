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

create or replace view public.balances
with (security_invoker = on) as
select wallet,
       sum(reward)::bigint as balance,
       count(*)::bigint as coins,
       max(created_at) as last_mined
from public.mined_coins
group by wallet;

-- The website can read everything but cannot write to any table directly.
alter table public.chain_state enable row level security;
alter table public.wallets enable row level security;
alter table public.mined_coins enable row level security;

drop policy if exists "public read" on public.chain_state;
drop policy if exists "public read" on public.wallets;
drop policy if exists "public read" on public.mined_coins;
create policy "public read" on public.chain_state for select to anon, authenticated using (true);
create policy "public read" on public.wallets for select to anon, authenticated using (true);
create policy "public read" on public.mined_coins for select to anon, authenticated using (true);

revoke all on public.chain_state, public.wallets, public.mined_coins, public.balances from anon, authenticated;
grant select on public.chain_state, public.wallets, public.mined_coins, public.balances to anon, authenticated;

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
