package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"strings"
	"testing"
)

func sign(privateKey ed25519.PrivateKey, tx Transaction) []byte {
	return ed25519.Sign(privateKey, signingPayload(tx))
}

func TestSubsidyHalvesAndSupplyIsCapped(t *testing.T) {
	if blockSubsidy(1) != 50 || blockSubsidy(halvingInterval-1) != 50 ||
		blockSubsidy(halvingInterval) != 25 || blockSubsidy(2*halvingInterval) != 12 {
		t.Fatal("unexpected subsidy schedule")
	}
	if blockSubsidy(64*halvingInterval) != 0 {
		t.Fatal("subsidy must reach zero")
	}
	var total uint64
	for era := uint64(0); era < 64; era++ {
		total += blockSubsidy(era*halvingInterval) * halvingInterval
	}
	if total != 20_370_000 {
		t.Fatalf("total supply = %d, want 20370000", total)
	}
}

func TestLeadingZeroBits(t *testing.T) {
	cases := map[string]int{
		"ff00":     0,
		"7f00":     1,
		"0fff":     4,
		"00ff":     8,
		"0001":     15,
		"00000000": 32,
	}
	for hash, want := range cases {
		if got := leadingZeroBits(hash); got != want {
			t.Errorf("leadingZeroBits(%s) = %d, want %d", hash, got, want)
		}
	}
	if leadingZeroBits("zz") != -1 {
		t.Error("invalid hex must report -1")
	}
}

func syntheticChain(length int, spacingMs int64, difficulty uint8) []Block {
	chain := []Block{genesisBlock()}
	for i := 1; i < length; i++ {
		prev := chain[i-1]
		chain = append(chain, Block{
			Index:      uint64(i),
			Timestamp:  prev.Timestamp + spacingMs,
			Difficulty: difficulty,
		})
	}
	return chain
}

func TestDifficultyRetargets(t *testing.T) {
	fast := syntheticChain(retargetInterval, 1000, 10)
	if got := expectedDifficulty(fast); got != 11 {
		t.Errorf("fast blocks: difficulty = %d, want 11", got)
	}
	slow := syntheticChain(retargetInterval, 5*targetBlockTimeMs, 10)
	if got := expectedDifficulty(slow); got != 9 {
		t.Errorf("slow blocks: difficulty = %d, want 9", got)
	}
	steady := syntheticChain(retargetInterval, targetBlockTimeMs, 10)
	if got := expectedDifficulty(steady); got != 10 {
		t.Errorf("on-target blocks: difficulty = %d, want 10", got)
	}
	mid := syntheticChain(retargetInterval-1, 1000, 10)
	if got := expectedDifficulty(mid); got != 10 {
		t.Errorf("mid-window difficulty = %d, want 10", got)
	}
	floor := syntheticChain(retargetInterval, 5*targetBlockTimeMs, minDifficulty)
	if got := expectedDifficulty(floor); got != minDifficulty {
		t.Errorf("difficulty fell below the minimum: %d", got)
	}
}

func TestForkChoiceUsesCumulativeWork(t *testing.T) {
	heavy := syntheticChain(3, 1, 20)
	light := syntheticChain(5, 1, 8)
	if chainWork(heavy).Cmp(chainWork(light)) <= 0 {
		t.Fatal("two blocks at difficulty 20 must outweigh four at difficulty 8")
	}
}

func TestRejectsWrongDifficultyAndLowFee(t *testing.T) {
	node, err := NewNode(t.TempDir() + "/chain.json")
	if err != nil {
		t.Fatal(err)
	}
	_, _, miner := testWallet(t)
	chain, _, _ := node.snapshot()
	block := Block{
		Index: 1, Timestamp: chain[0].Timestamp + 1, PreviousHash: chain[0].Hash,
		Miner: miner, Transactions: []Transaction{}, Difficulty: initialDifficulty - 1,
	}
	for {
		block.Hash = blockHash(block)
		if leadingZeroBits(block.Hash) >= int(block.Difficulty) {
			break
		}
		block.Nonce++
	}
	if err := node.receiveBlock(block); err == nil || !strings.Contains(err.Error(), "difficulty") {
		t.Fatalf("block with easier difficulty was accepted: %v", err)
	}

	publicKey, privateKey, sender := testWallet(t)
	_, _, recipient := testWallet(t)
	if _, err := node.mine(sender); err != nil {
		t.Fatal(err)
	}
	tx := signedTransaction(privateKey, publicKey, sender, recipient, 5, 1)
	tx.Fee = 0
	tx.Signature = hex.EncodeToString(sign(privateKey, tx))
	if err := node.addTransaction(tx); err == nil {
		t.Fatal("zero-fee transaction was accepted")
	}
}

func TestMinedBlockPaysSubsidyPlusFees(t *testing.T) {
	node, err := NewNode(t.TempDir() + "/chain.json")
	if err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, sender := testWallet(t)
	_, _, recipient := testWallet(t)
	_, _, miner := testWallet(t)
	if _, err := node.mine(sender); err != nil {
		t.Fatal(err)
	}
	tx := signedTransaction(privateKey, publicKey, sender, recipient, 10, 1)
	tx.Fee = 7
	tx.Signature = hex.EncodeToString(sign(privateKey, tx))
	if err := node.addTransaction(tx); err != nil {
		t.Fatal(err)
	}
	if _, err := node.mine(miner); err != nil {
		t.Fatal(err)
	}
	_, balances, _ := node.snapshot()
	if balances[miner] != initialBlockReward+7 || balances[sender] != initialBlockReward-17 || balances[recipient] != 10 {
		t.Fatalf("unexpected balances: %#v", balances)
	}
	var total uint64
	for _, balance := range balances {
		total += balance
	}
	if total != 2*initialBlockReward {
		t.Fatalf("total supply %d != issued subsidy %d", total, 2*initialBlockReward)
	}
}
