package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	initialBlockReward = uint64(50)
	halvingInterval    = uint64(210000)
	minTxFee           = uint64(1)

	genesisTimestamp  = int64(1793900000000)
	initialDifficulty = uint8(8)
	minDifficulty     = uint8(4)
	maxDifficulty     = uint8(40)
	retargetInterval  = 10
	targetBlockTimeMs = int64(30000)

	maxBlockTransactions   = 500
	maxPendingTransactions = 500
	maxClockDrift          = 2 * time.Hour
)

type Transaction struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Amount    uint64 `json:"amount"`
	Fee       uint64 `json:"fee"`
	Nonce     uint64 `json:"nonce"`
	PublicKey string `json:"publicKey"`
	Signature string `json:"signature"`
}

type Block struct {
	Index        uint64        `json:"index"`
	Timestamp    int64         `json:"timestamp"`
	PreviousHash string        `json:"previousHash"`
	Miner        string        `json:"miner"`
	Transactions []Transaction `json:"transactions"`
	Difficulty   uint8         `json:"difficulty"`
	Nonce        uint64        `json:"nonce"`
	Hash         string        `json:"hash"`
}

// blockSubsidy halves every halvingInterval blocks, so total supply is capped (see PROTOCOL.md).
func blockSubsidy(index uint64) uint64 {
	halvings := index / halvingInterval
	if halvings >= 64 {
		return 0
	}
	return initialBlockReward >> halvings
}

func leadingZeroBits(hexHash string) int {
	raw, err := hex.DecodeString(hexHash)
	if err != nil {
		return -1
	}
	bits := 0
	for _, b := range raw {
		if b == 0 {
			bits += 8
			continue
		}
		bits += bits8(b)
		break
	}
	return bits
}

func bits8(b byte) int {
	n := 0
	for mask := byte(0x80); mask != 0 && b&mask == 0; mask >>= 1 {
		n++
	}
	return n
}

// expectedDifficulty is the difficulty the block following chain must carry.
func expectedDifficulty(chain []Block) uint8 {
	last := chain[len(chain)-1]
	next := len(chain)
	if next%retargetInterval != 0 {
		return last.Difficulty
	}
	timespan := last.Timestamp - chain[next-retargetInterval].Timestamp
	expected := int64(retargetInterval-1) * targetBlockTimeMs
	switch {
	case timespan < expected/2 && last.Difficulty < maxDifficulty:
		return last.Difficulty + 1
	case timespan > expected*2 && last.Difficulty > minDifficulty:
		return last.Difficulty - 1
	}
	return last.Difficulty
}

// chainWork is the total expected hashing work, summing 2^difficulty per block.
func chainWork(chain []Block) *big.Int {
	total := new(big.Int)
	for _, block := range chain[1:] {
		total.Add(total, new(big.Int).Lsh(big.NewInt(1), uint(block.Difficulty)))
	}
	return total
}

type diskState struct {
	Chain []Block `json:"chain"`
}

type Node struct {
	mu       sync.Mutex
	chain    []Block
	balances map[string]uint64
	nonces   map[string]uint64
	pending  []Transaction
	dataFile string
}

func genesisBlock() Block {
	return Block{Index: 0, Timestamp: genesisTimestamp, PreviousHash: "0", Miner: "RockyCoin", Transactions: []Transaction{}, Difficulty: initialDifficulty, Hash: "genesis"}
}

func NewNode(dataFile string) (*Node, error) {
	n := &Node{
		chain:    []Block{genesisBlock()},
		balances: make(map[string]uint64),
		nonces:   make(map[string]uint64),
		dataFile: dataFile,
	}
	data, err := os.ReadFile(dataFile)
	if errors.Is(err, os.ErrNotExist) {
		return n, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read chain state: %w", err)
	}
	var saved diskState
	if err := json.Unmarshal(data, &saved); err != nil {
		return nil, fmt.Errorf("decode chain state: %w", err)
	}
	if err := n.loadChain(saved.Chain); err != nil {
		return nil, fmt.Errorf("validate saved chain: %w", err)
	}
	return n, nil
}

func addressFromPublicKey(publicKey ed25519.PublicKey) string {
	sum := sha256.Sum256(publicKey)
	return "RC" + hex.EncodeToString(sum[:20])
}

func validAddress(address string) bool {
	if len(address) != 42 || !strings.HasPrefix(address, "RC") {
		return false
	}
	_, err := hex.DecodeString(address[2:])
	return err == nil
}

func signingPayload(tx Transaction) []byte {
	data, _ := json.Marshal(struct {
		From   string `json:"from"`
		To     string `json:"to"`
		Amount uint64 `json:"amount"`
		Fee    uint64 `json:"fee"`
		Nonce  uint64 `json:"nonce"`
	}{tx.From, tx.To, tx.Amount, tx.Fee, tx.Nonce})
	return data
}

func verifyTransaction(tx Transaction) error {
	if !validAddress(tx.From) || !validAddress(tx.To) || tx.Amount == 0 {
		return errors.New("invalid sender, recipient, or amount")
	}
	if tx.Fee < minTxFee {
		return fmt.Errorf("fee below minimum of %d", minTxFee)
	}
	if tx.Amount > ^uint64(0)-tx.Fee {
		return errors.New("amount plus fee overflows")
	}
	publicKey, err := hex.DecodeString(tx.PublicKey)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return errors.New("invalid public key")
	}
	if addressFromPublicKey(publicKey) != tx.From {
		return errors.New("public key does not match sender")
	}
	signature, err := hex.DecodeString(tx.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize ||
		!ed25519.Verify(publicKey, signingPayload(tx), signature) {
		return errors.New("invalid transaction signature")
	}
	return nil
}

func blockHash(block Block) string {
	data, _ := json.Marshal(struct {
		Index        uint64        `json:"index"`
		Timestamp    int64         `json:"timestamp"`
		PreviousHash string        `json:"previousHash"`
		Miner        string        `json:"miner"`
		Transactions []Transaction `json:"transactions"`
		Difficulty   uint8         `json:"difficulty"`
		Nonce        uint64        `json:"nonce"`
	}{block.Index, block.Timestamp, block.PreviousHash, block.Miner, block.Transactions, block.Difficulty, block.Nonce})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// checkBlock validates block as the successor of chain and applies it to balances and nonces.
func checkBlock(chain []Block, block Block, balances, nonces map[string]uint64) error {
	previous := chain[len(chain)-1]
	if block.Index != previous.Index+1 || block.PreviousHash != previous.Hash {
		return errors.New("block does not extend its predecessor")
	}
	if block.Difficulty != expectedDifficulty(chain) {
		return errors.New("wrong difficulty for this height")
	}
	if block.Hash != blockHash(block) || leadingZeroBits(block.Hash) < int(block.Difficulty) {
		return errors.New("invalid proof of work")
	}
	if !validAddress(block.Miner) {
		return errors.New("invalid miner address")
	}
	if block.Timestamp < previous.Timestamp || block.Timestamp > time.Now().Add(maxClockDrift).UnixMilli() {
		return errors.New("invalid block timestamp")
	}
	if len(block.Transactions) > maxBlockTransactions {
		return errors.New("too many transactions in block")
	}
	payout := blockSubsidy(block.Index)
	for _, tx := range block.Transactions {
		if err := applyTransaction(tx, balances, nonces); err != nil {
			return fmt.Errorf("invalid transaction: %w", err)
		}
		payout += tx.Fee
	}
	if balances[block.Miner] > ^uint64(0)-payout {
		return errors.New("miner balance overflow")
	}
	balances[block.Miner] += payout
	return nil
}

// validateChain fully verifies a chain from genesis and returns the resulting account state.
func validateChain(chain []Block) (map[string]uint64, map[string]uint64, error) {
	if len(chain) == 0 || !equalBlocks(chain[0], genesisBlock()) {
		return nil, nil, errors.New("missing or invalid genesis block")
	}
	balances := make(map[string]uint64)
	nonces := make(map[string]uint64)
	for i := 1; i < len(chain); i++ {
		if err := checkBlock(chain[:i], chain[i], balances, nonces); err != nil {
			return nil, nil, fmt.Errorf("invalid block %d: %w", i, err)
		}
	}
	return balances, nonces, nil
}

func (n *Node) loadChain(chain []Block) error {
	balances, nonces, err := validateChain(chain)
	if err != nil {
		return err
	}
	n.chain = append([]Block(nil), chain...)
	n.balances = balances
	n.nonces = nonces
	return nil
}

// adopt installs an already-validated chain, persists it, and keeps still-valid pending transactions.
func (n *Node) adopt(chain []Block, balances, nonces map[string]uint64) error {
	if err := n.persist(chain); err != nil {
		return err
	}
	n.chain = chain
	n.balances = balances
	n.nonces = nonces

	pendingBalances := cloneBalances(balances)
	pendingNonces := cloneBalances(nonces)
	var kept []Transaction
	for _, tx := range n.pending {
		if applyTransaction(tx, pendingBalances, pendingNonces) == nil {
			kept = append(kept, tx)
		}
	}
	n.pending = kept
	return nil
}

var (
	errKnownBlock   = errors.New("block already known")
	errNotExtending = errors.New("block does not extend the current chain tip")
)

// receiveBlock accepts a block from a peer only if it extends the current tip.
func (n *Node) receiveBlock(block Block) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	tip := n.chain[len(n.chain)-1]
	if block.Hash == tip.Hash {
		return errKnownBlock
	}
	if block.Index != tip.Index+1 || block.PreviousHash != tip.Hash {
		return errNotExtending
	}
	balances := cloneBalances(n.balances)
	nonces := cloneBalances(n.nonces)
	if err := checkBlock(n.chain, block, balances, nonces); err != nil {
		return err
	}
	return n.adopt(append(append([]Block{}, n.chain...), block), balances, nonces)
}

// replaceChain adopts a fully valid chain with strictly more cumulative work than the current one.
func (n *Node) replaceChain(chain []Block) (bool, error) {
	balances, nonces, err := validateChain(chain)
	if err != nil {
		return false, err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if chainWork(chain).Cmp(chainWork(n.chain)) <= 0 {
		return false, nil
	}
	if err := n.adopt(append([]Block(nil), chain...), balances, nonces); err != nil {
		return false, err
	}
	return true, nil
}

func equalBlocks(a, b Block) bool {
	aJSON, _ := json.Marshal(a)
	bJSON, _ := json.Marshal(b)
	return string(aJSON) == string(bJSON)
}

func applyTransaction(tx Transaction, balances, nonces map[string]uint64) error {
	if err := verifyTransaction(tx); err != nil {
		return err
	}
	if tx.Nonce != nonces[tx.From]+1 {
		return errors.New("incorrect transaction nonce")
	}
	if balances[tx.From] < tx.Amount+tx.Fee {
		return errors.New("insufficient balance")
	}
	if balances[tx.To] > ^uint64(0)-tx.Amount {
		return errors.New("recipient balance overflow")
	}
	balances[tx.From] -= tx.Amount + tx.Fee
	balances[tx.To] += tx.Amount
	nonces[tx.From] = tx.Nonce
	return nil
}

func (n *Node) addTransaction(tx Transaction) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := verifyTransaction(tx); err != nil {
		return err
	}
	balances := cloneBalances(n.balances)
	nonces := cloneBalances(n.nonces)
	for _, pending := range n.pending {
		if err := applyTransaction(pending, balances, nonces); err != nil {
			return errors.New("pending transaction state is invalid")
		}
	}
	if len(n.pending) >= maxPendingTransactions {
		return errors.New("transaction pool is full")
	}
	if err := applyTransaction(tx, balances, nonces); err != nil {
		return err
	}
	n.pending = append(n.pending, tx)
	return nil
}

func cloneBalances(source map[string]uint64) map[string]uint64 {
	clone := make(map[string]uint64, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func (n *Node) mine(miner string) (Block, error) {
	if !validAddress(miner) {
		return Block{}, errors.New("invalid miner address")
	}
	// Search for the proof of work without holding the lock so the node stays responsive.
	n.mu.Lock()
	previous := n.chain[len(n.chain)-1]
	block := Block{
		Index:        uint64(len(n.chain)),
		PreviousHash: previous.Hash,
		Miner:        miner,
		Transactions: append([]Transaction{}, n.pending...),
		Difficulty:   expectedDifficulty(n.chain),
	}
	n.mu.Unlock()
	if len(block.Transactions) > maxBlockTransactions {
		block.Transactions = block.Transactions[:maxBlockTransactions]
	}
	block.Timestamp = time.Now().UnixMilli()
	if block.Timestamp < previous.Timestamp {
		block.Timestamp = previous.Timestamp
	}
	for {
		block.Hash = blockHash(block)
		if leadingZeroBits(block.Hash) >= int(block.Difficulty) {
			break
		}
		block.Nonce++
	}

	n.mu.Lock()
	defer n.mu.Unlock()
	if n.chain[len(n.chain)-1].Hash != previous.Hash {
		return Block{}, errors.New("chain advanced while mining; try again")
	}
	balances := cloneBalances(n.balances)
	nonces := cloneBalances(n.nonces)
	if err := checkBlock(n.chain, block, balances, nonces); err != nil {
		return Block{}, err
	}
	if err := n.adopt(append(append([]Block{}, n.chain...), block), balances, nonces); err != nil {
		return Block{}, err
	}
	return block, nil
}

func (n *Node) persist(chain []Block) error {
	data, err := json.MarshalIndent(diskState{Chain: chain}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode chain state: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(n.dataFile), 0o700); err != nil {
		return fmt.Errorf("create chain data directory: %w", err)
	}
	temp := n.dataFile + ".tmp"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return fmt.Errorf("write chain state: %w", err)
	}
	if err := os.Rename(temp, n.dataFile); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("replace chain state: %w", err)
	}
	return nil
}

func (n *Node) snapshot() ([]Block, map[string]uint64, []Transaction) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]Block(nil), n.chain...), cloneBalances(n.balances), append([]Transaction(nil), n.pending...)
}
