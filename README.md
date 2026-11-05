# RockyCoin local testnet prototype

This repository now includes a small, single-node RockyCoin testnet prototype. It is for local development only: it is not decentralized, has no peer network, and its tokens have no monetary value. Do not reuse its keys or deploy it as a public or production network.

## Run locally

Requires Go 1.24 or newer:

```sh
go test ./...
go run .
```

The node listens only on `127.0.0.1:8080` by default and stores its chain in `rocky-testnet.json`. You can change the data file with `ROCKY_DATA_FILE`; `ROCKY_ADDR` may select another loopback address, but non-loopback binds are refused.

## API

- `GET /api/health` — node status and height
- `GET /api/chain` — blocks and pending transactions
- `GET /api/balance?address=RC...` — local testnet balance
- `POST /api/wallet/new` — generate an Ed25519 keypair
- `POST /api/transactions` — submit a signed transfer
- `POST /api/mine` — mine a block and receive the fixed 50 ROCKY testnet reward

Create a wallet with `curl -X POST http://127.0.0.1:8080/api/wallet/new`. The response contains a private key so the owner can sign transfers. It is returned only once and is not stored by the node; protect it and never share it. A new wallet can mine a block to obtain local testnet tokens.

Transactions use integer ROCKY amounts, sequential account nonces, and Ed25519 signatures. Blocks use SHA-256 proof of work with a deliberately low testnet difficulty. Chain state is validated and rebuilt from disk at startup.

This is a prototype, not an audited blockchain implementation. It has no peer discovery, network consensus, difficulty adjustment, authenticated API, faucet controls, or production key management. The loopback-only server restriction is intentional. A public testnet requires a reviewed protocol, peer/validator implementation, hosting, operational monitoring, and security review first.