package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"testing"
)

func testWallet(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey, string) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return publicKey, privateKey, addressFromPublicKey(publicKey)
}

func signedTransaction(privateKey ed25519.PrivateKey, publicKey ed25519.PublicKey, from, to string, amount, nonce uint64) Transaction {
	tx := Transaction{
		From:      from,
		To:        to,
		Amount:    amount,
		Nonce:     nonce,
		PublicKey: hex.EncodeToString(publicKey),
	}
	tx.Signature = hex.EncodeToString(ed25519.Sign(privateKey, signingPayload(tx)))
	return tx
}

func TestLocalTestnetTransferAndPersistence(t *testing.T) {
	path := t.TempDir() + "/chain.json"
	node, err := NewNode(path)
	if err != nil {
		t.Fatal(err)
	}
	senderPublicKey, senderPrivateKey, sender := testWallet(t)
	_, _, recipient := testWallet(t)
	_, _, miner := testWallet(t)

	if _, err := node.mine(sender); err != nil {
		t.Fatal(err)
	}
	tx := signedTransaction(senderPrivateKey, senderPublicKey, sender, recipient, 20, 1)
	if err := node.addTransaction(tx); err != nil {
		t.Fatal(err)
	}
	overspend := signedTransaction(senderPrivateKey, senderPublicKey, sender, recipient, 31, 2)
	if err := node.addTransaction(overspend); err == nil {
		t.Fatal("expected overspending transaction to be rejected")
	}
	if _, err := node.mine(miner); err != nil {
		t.Fatal(err)
	}

	chain, balances, pending := node.snapshot()
	if len(chain) != 3 || len(pending) != 0 {
		t.Fatalf("unexpected chain/pending lengths: %d/%d", len(chain), len(pending))
	}
	if balances[sender] != 30 || balances[recipient] != 20 || balances[miner] != blockReward {
		t.Fatalf("unexpected balances: %#v", balances)
	}

	reloaded, err := NewNode(path)
	if err != nil {
		t.Fatal(err)
	}
	reloadedChain, reloadedBalances, _ := reloaded.snapshot()
	if len(reloadedChain) != len(chain) || reloadedBalances[sender] != 30 ||
		reloadedBalances[recipient] != 20 || reloadedBalances[miner] != blockReward {
		t.Fatalf("reloaded chain state differs: %#v", reloadedBalances)
	}
}

func TestRejectsTamperedTransactions(t *testing.T) {
	node, err := NewNode(t.TempDir() + "/chain.json")
	if err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, sender := testWallet(t)
	_, _, recipient := testWallet(t)
	tx := signedTransaction(privateKey, publicKey, sender, recipient, 1, 1)
	tx.Amount = 2
	if err := node.addTransaction(tx); err == nil {
		t.Fatal("expected tampered transaction to be rejected")
	}
}
