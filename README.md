# RockyCoin local testnet prototype

This repository includes a small RockyCoin testnet prototype: a proof-of-work blockchain whose nodes gossip transactions and blocks to peers and converge on the longest valid chain. It is for local development only. Nodes talk to each other over loopback HTTP, and its tokens have no monetary value. Do not reuse its keys or deploy it as a public or production network.

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
- `POST /api/mine` — mine a block and receive the fixed 50 ROCKY testnet reward
- `GET /api/peers`, `POST /api/peers` (`{"url":"http://127.0.0.1:8082"}`) — list or add peers
- `POST /api/blocks` — receive a block from a peer

Create a wallet with `curl -X POST http://127.0.0.1:8080/api/wallet/new`. The response contains a private key so the owner can sign transfers. It is returned only once and is not stored by the node; protect it and never share it. A new wallet can mine a block to obtain local testnet tokens.

Transactions use integer ROCKY amounts, sequential account nonces, and Ed25519 signatures. Blocks use SHA-256 proof of work with a deliberately low testnet difficulty. Chain state is validated and rebuilt from disk at startup.

## Running several nodes

```sh
ROCKY_ADDR=127.0.0.1:8081 ROCKY_DATA_FILE=a.json go run .
ROCKY_ADDR=127.0.0.1:8082 ROCKY_DATA_FILE=b.json ROCKY_PEERS=http://127.0.0.1:8081 go run .
```

New transactions and mined blocks are broadcast to peers. A block that extends a node's tip is verified (proof of work, timestamps, signatures, nonces, balances) and appended. Every ten seconds, and whenever a peer announces a block that does not fit, a node downloads peer chains, fully re-validates them from genesis, and adopts the longest valid one, so forks resolve by reorganization. Pending transactions that stay valid are kept across a reorganization. Peer URLs are limited to loopback HTTP origins.

This is a prototype, not an audited blockchain implementation. It has no automatic peer discovery, cumulative-work fork choice (it uses chain length at a fixed difficulty), difficulty adjustment, transaction fees, authenticated API, faucet controls, or production key management. The loopback-only server restriction is intentional. A public testnet requires a reviewed protocol, hardened peer-to-peer protocol, hosting, operational monitoring, and security review first.