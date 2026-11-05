package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	blockReward = uint64(50)
	difficulty  = "00"
)

type Transaction struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Amount    uint64 `json:"amount"`
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
	Nonce        uint64        `json:"nonce"`
	Hash         string        `json:"hash"`
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
	return Block{Index: 0, Timestamp: 0, PreviousHash: "0", Miner: "RockyCoin", Transactions: []Transaction{}, Hash: "genesis"}
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
		Nonce  uint64 `json:"nonce"`
	}{tx.From, tx.To, tx.Amount, tx.Nonce})
	return data
}

func verifyTransaction(tx Transaction) error {
	if !validAddress(tx.From) || !validAddress(tx.To) || tx.Amount == 0 {
		return errors.New("invalid sender, recipient, or amount")
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
		Nonce        uint64        `json:"nonce"`
	}{block.Index, block.Timestamp, block.PreviousHash, block.Miner, block.Transactions, block.Nonce})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (n *Node) loadChain(chain []Block) error {
	if len(chain) == 0 || !equalBlocks(chain[0], genesisBlock()) {
		return errors.New("missing or invalid genesis block")
	}
	balances := make(map[string]uint64)
	nonces := make(map[string]uint64)
	for i := 1; i < len(chain); i++ {
		block := chain[i]
		previous := chain[i-1]
		if block.Index != uint64(i) || block.PreviousHash != previous.Hash ||
			block.Hash != blockHash(block) || !strings.HasPrefix(block.Hash, difficulty) ||
			!validAddress(block.Miner) {
			return fmt.Errorf("invalid block %d", i)
		}
		for _, tx := range block.Transactions {
			if err := applyTransaction(tx, balances, nonces); err != nil {
				return fmt.Errorf("invalid transaction in block %d: %w", i, err)
			}
		}
		if balances[block.Miner] > ^uint64(0)-blockReward {
			return fmt.Errorf("reward overflow in block %d", i)
		}
		balances[block.Miner] += blockReward
	}
	n.chain = append([]Block(nil), chain...)
	n.balances = balances
	n.nonces = nonces
	return nil
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
	if balances[tx.From] < tx.Amount {
		return errors.New("insufficient balance")
	}
	if balances[tx.To] > ^uint64(0)-tx.Amount {
		return errors.New("recipient balance overflow")
	}
	balances[tx.From] -= tx.Amount
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
	n.mu.Lock()
	defer n.mu.Unlock()
	previous := n.chain[len(n.chain)-1]
	block := Block{
		Index:        uint64(len(n.chain)),
		Timestamp:    time.Now().UnixMilli(),
		PreviousHash: previous.Hash,
		Miner:        miner,
		Transactions: append([]Transaction{}, n.pending...),
	}
	for {
		block.Hash = blockHash(block)
		if strings.HasPrefix(block.Hash, difficulty) {
			break
		}
		block.Nonce++
	}

	balances := cloneBalances(n.balances)
	nonces := cloneBalances(n.nonces)
	for _, tx := range block.Transactions {
		if err := applyTransaction(tx, balances, nonces); err != nil {
			return Block{}, fmt.Errorf("pending transaction became invalid: %w", err)
		}
	}
	if balances[miner] > ^uint64(0)-blockReward {
		return Block{}, errors.New("miner balance overflow")
	}
	balances[miner] += blockReward

	nextChain := append(append([]Block{}, n.chain...), block)
	if err := n.persist(nextChain); err != nil {
		return Block{}, err
	}
	n.chain = nextChain
	n.balances = balances
	n.nonces = nonces
	n.pending = nil
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
