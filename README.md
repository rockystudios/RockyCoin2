# RockyCoin local testnet prototype

This repository includes a small RockyCoin testnet prototype: a proof-of-work blockchain whose nodes gossip transactions and blocks to peers and converge on the chain with the most cumulative work. The rules are written down in [PROTOCOL.md](PROTOCOL.md). It is for local development only. Nodes talk to each other over loopback HTTP, and its tokens have no monetary value. Do not reuse its keys or deploy it as a public or production network. Chains created before the consensus rules in PROTOCOL.md are incompatible; delete old data files.

## Run locally

Requires Go 1.24 or newer:

```sh
go test ./...
go run .
```

The node listens only on `127.0.0.1:8080` by default and stores its chain in `rocky-testnet.json`. You can change the data file with `ROCKY_DATA_FILE`; `ROCKY_ADDR` may select another loopback address, but non-loopback binds are refused. `ROCKY_PEERS` takes a comma-separated list of loopback peer URLs, each with an explicit port.

## API

- `GET /api/health` — node status and height
- `GET /api/chain` — blocks and pending transactions
- `GET /api/balance?address=RC...` — local testnet balance
- `POST /api/wallet/new` — generate an Ed25519 keypair
- `POST /api/transactions` — submit a signed transfer
- `POST /api/mine` — mine a block; the miner earns the halving block subsidy (50 ROCKY at first) plus transaction fees
- `GET /api/peers`, `POST /api/peers` (`{"url":"http://127.0.0.1:8082"}`) — list or add peers
- `POST /api/blocks` — receive a block from a peer

Create a wallet with `curl -X POST http://127.0.0.1:8080/api/wallet/new`. The response contains a private key so the owner can sign transfers. It is returned only once and is not stored by the node; protect it and never share it. A new wallet can mine a block to obtain local testnet tokens.

Transactions use integer ROCKY amounts, a minimum fee of 1 ROCKY paid to the miner, sequential account nonces, and Ed25519 signatures (the signature covers the fee). Blocks use SHA-256 proof of work measured in leading zero bits; difficulty retargets every 10 blocks toward a 30-second block time. Total issuance is capped at 20,370,000 ROCKY. Chain state is validated and rebuilt from disk at startup.

## Running several nodes

```sh
ROCKY_ADDR=127.0.0.1:8081 ROCKY_DATA_FILE=a.json go run .
ROCKY_ADDR=127.0.0.1:8082 ROCKY_DATA_FILE=b.json ROCKY_PEERS=http://127.0.0.1:8081 go run .
```

New transactions and mined blocks are broadcast to peers. A block that extends a node's tip is verified (proof of work, timestamps, signatures, nonces, balances) and appended. Every ten seconds, and whenever a peer announces a block that does not fit, a node downloads peer chains, fully re-validates them from genesis, and adopts the chain with the most cumulative work, so forks resolve by reorganization. Pending transactions that stay valid are kept across a reorganization. Peer URLs are limited to loopback HTTP origins.

This is a prototype, not an audited blockchain implementation. It has no automatic peer discovery, authenticated API, faucet controls, or production key management. The loopback-only server restriction is intentional. See the "Known gaps" list in [PROTOCOL.md](PROTOCOL.md): a public testnet still needs hardened peer-to-peer networking, independent miners and nodes, wallet software, hosting and monitoring, an independent security audit, and legal review.
## Web miner (Supabase ledger)

`index.html` is a browser miner. A Web Worker searches for a SHA-256 hash of
`wallet:tip:nonce` with leading zeros and submits it to Supabase, which verifies
the proof, assigns a random reward (1-10 ROCKY) and stores the coin with its hash.
Mining only runs while the tab is open.

Setup:
1. Run `supabase/schema.sql` in the Supabase SQL editor.
2. Put the project URL and anon/publishable key in `CONFIG` at the top of the
   second `<script>` in `index.html`. Never use the `service_role` key.
3. Tune `difficulty` and `min_seconds_between_coins` in the `chain_state` table.

Wallets: each browser creates a secret key; the address is derived from it
(`RC` + first 40 hex of SHA-256(`rocky-address:` + secret)). You can name a wallet
and send ROCKY to an address or `@name`. The secret is sent to Supabase over HTTPS
when you send or rename, so the ledger operator could in principle see it.
Back up your secret key, because losing it means losing the wallet.

Re-run `supabase/schema.sql` after updating; it is safe to run again.

The ledger is centralized in Supabase and ROCKY has no monetary value.
