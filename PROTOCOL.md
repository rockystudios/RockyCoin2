# RockyCoin protocol (testnet draft v0.1)

Status: **draft for the local testnet**. Values below are implemented in code but are not final, audited, or suitable for a public or production network. Tokens have no monetary value.

## Accounts and transactions

- Address: `RC` + hex of the first 20 bytes of `SHA-256(Ed25519 public key)`.
- Transaction fields: `from`, `to`, `amount`, `fee`, `nonce`, `publicKey`, `signature`. All amounts are unsigned 64-bit integers of the smallest unit (1 ROCKY).
- The signature covers `{from, to, amount, fee, nonce}` (JSON). The public key must hash to `from`.
- `nonce` must equal the sender's last confirmed nonce + 1 (prevents replay and reordering).
- `fee >= 1`. Sender is debited `amount + fee`; recipient is credited `amount`; the fee goes to the block's miner.

## Blocks

- Fields: `index`, `timestamp` (ms), `previousHash`, `miner`, `transactions`, `difficulty`, `nonce`, `hash`.
- `hash = SHA-256(JSON{index, timestamp, previousHash, miner, transactions, difficulty, nonce})`.
- Proof of work: `hash` must have at least `difficulty` leading zero **bits**.
- Timestamp: not earlier than the parent and at most 2 hours in the future.
- At most 500 transactions per block.
- The genesis block is hard-coded (index 0, timestamp 1793900000000, difficulty 8).

## Monetary policy

- Block subsidy starts at 50 ROCKY and halves every 210,000 blocks (integer shift), reaching 0 after 64 halvings.
- Because of integer halving, **total issuance is capped at 20,370,000 ROCKY** (verified by a test).
- Miner receives `subsidy + sum(fees)`. No other issuance exists.

## Difficulty adjustment

- Target block time: 30 seconds. Retarget every 10 blocks.
- At each retarget height, compare the time spanned by the previous 10 blocks with `9 × 30 s`. If it was under half the target, difficulty +1 bit; if over double, difficulty −1 bit; otherwise unchanged. Bounds: 4 to 40 bits.
- Every block must carry exactly the difficulty derived from its ancestors; nodes reject anything else.

## Fork choice

- The valid chain with the greatest **cumulative work** (sum of `2^difficulty` over blocks) wins, not simply the longest chain.
- Peers are re-validated from genesis before a reorganization; pending transactions that remain valid are kept.

## Networking (testnet)

- HTTP/JSON between nodes: `POST /api/blocks`, `POST /api/transactions`, `GET /api/chain`, `GET/POST /api/peers`.
- Nodes broadcast new blocks and transactions, and sync peer chains every 10 seconds.
- **Prototype limits:** peers and binds are loopback only, full chain download for sync, no peer authentication or rate limiting, no transport encryption.

## Known gaps before any public network

1. Open, hardened P2P (peer discovery, headers-first sync, rate limits, ban scoring, TLS or a Noise-style transport).
2. Memory-hard or otherwise ASIC-considered PoW decision, and a finer difficulty algorithm (the ±1 bit step is coarse).
3. Fee-priority mempool, replace-by-fee rules, and per-sender pool limits.
4. Wallet software with encrypted key storage, mnemonic backup, and offline signing. The API currently returns raw private keys.
5. Chain pruning and a binary encoding in place of JSON.
6. Independent security audit and a long-running public testnet with third-party miners.
7. Legal and regulatory review for the jurisdictions involved before any token is distributed.
8. Hosting, monitoring, seed nodes, and incident response.
