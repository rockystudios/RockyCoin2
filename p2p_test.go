package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestServer(t *testing.T) (*server, *httptest.Server) {
	t.Helper()
	node, err := NewNode(t.TempDir() + "/chain.json")
	if err != nil {
		t.Fatal(err)
	}
	app := &server{node: node, peers: newPeerSet()}
	httpServer := httptest.NewServer(app.routes())
	t.Cleanup(httpServer.Close)
	app.self = httpServer.URL
	return app, httpServer
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

func chainLength(app *server) int {
	chain, _, _ := app.node.snapshot()
	return len(chain)
}

func TestMinedBlockAndTransactionGossip(t *testing.T) {
	a, aServer := newTestServer(t)
	b, bServer := newTestServer(t)
	if _, err := a.peers.add(bServer.URL, a.self); err != nil {
		t.Fatal(err)
	}
	if _, err := b.peers.add(aServer.URL, b.self); err != nil {
		t.Fatal(err)
	}

	publicKey, privateKey, sender := testWallet(t)
	_, _, recipient := testWallet(t)
	if _, err := a.node.mine(sender); err != nil {
		t.Fatal(err)
	}
	a.peers.broadcast("/api/blocks", func() Block { chain, _, _ := a.node.snapshot(); return chain[1] }())
	waitFor(t, func() bool { return chainLength(b) == 2 })

	tx := signedTransaction(privateKey, publicKey, sender, recipient, 10, 1)
	body, _ := json.Marshal(tx)
	response, err := http.Post(bServer.URL+"/api/transactions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("unexpected status %s", response.Status)
	}
	waitFor(t, func() bool { _, _, pending := a.node.snapshot(); return len(pending) == 1 })
}

func TestForkResolvesToLongestValidChain(t *testing.T) {
	a, _ := newTestServer(t)
	b, bServer := newTestServer(t)
	_, _, minerA := testWallet(t)
	_, _, minerB := testWallet(t)

	if _, err := a.node.mine(minerA); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := b.node.mine(minerB); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.peers.add(bServer.URL, a.self); err != nil {
		t.Fatal(err)
	}
	if !a.syncOnce() {
		t.Fatal("expected sync to adopt the longer chain")
	}
	chain, balances, _ := a.node.snapshot()
	if len(chain) != 3 || balances[minerA] != 0 || balances[minerB] != 2*initialBlockReward {
		t.Fatalf("unexpected state after reorg: len=%d balances=%#v", len(chain), balances)
	}
}

func TestRejectsInvalidReplacementChains(t *testing.T) {
	a, _ := newTestServer(t)
	b, _ := newTestServer(t)
	_, _, miner := testWallet(t)
	for i := 0; i < 2; i++ {
		if _, err := b.node.mine(miner); err != nil {
			t.Fatal(err)
		}
	}
	chain, _, _ := b.node.snapshot()

	tampered := append([]Block(nil), chain...)
	tampered[2].Miner = "RC" + "00000000000000000000000000000000000000ff"
	if replaced, err := a.node.replaceChain(tampered); replaced || err == nil {
		t.Fatalf("tampered chain was accepted: replaced=%v err=%v", replaced, err)
	}

	forgedReward := append([]Block(nil), chain...)
	forgedReward[1].Transactions = []Transaction{{From: miner, To: miner, Amount: 1}}
	if replaced, err := a.node.replaceChain(forgedReward); replaced || err == nil {
		t.Fatal("chain containing an unsigned transaction was accepted")
	}
	if chainLength(a) != 1 {
		t.Fatal("node chain changed after rejecting invalid replacements")
	}
}

func TestPeerURLsMustBeLoopback(t *testing.T) {
	for _, raw := range []string{
		"http://example.com:8080", "https://127.0.0.1:8080", "http://127.0.0.1",
		"http://10.0.0.1:8080", "http://user@127.0.0.1:8080", "http://127.0.0.1:8080/path",
	} {
		if _, err := normalizePeer(raw); err == nil {
			t.Errorf("expected %q to be rejected", raw)
		}
	}
	for _, raw := range []string{"http://127.0.0.1:8080", "http://localhost:9000/", "http://[::1]:8081"} {
		if _, err := normalizePeer(raw); err != nil {
			t.Errorf("expected %q to be accepted: %v", raw, err)
		}
	}
}
