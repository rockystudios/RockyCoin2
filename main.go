package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

type server struct {
	node *Node
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/chain", s.chain)
	mux.HandleFunc("GET /api/balance", s.balance)
	mux.HandleFunc("POST /api/wallet/new", s.newWallet)
	mux.HandleFunc("POST /api/transactions", s.addTransaction)
	mux.HandleFunc("POST /api/mine", s.mine)
	return mux
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func (s *server) health(w http.ResponseWriter, _ *http.Request) {
	chain, _, pending := s.node.snapshot()
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "ok",
		"network":    "rocky-testnet-local",
		"blocks":     len(chain),
		"pendingTxs": len(pending),
	})
}

func (s *server) chain(w http.ResponseWriter, _ *http.Request) {
	chain, _, pending := s.node.snapshot()
	writeJSON(w, http.StatusOK, map[string]any{"chain": chain, "pending": pending})
}

func (s *server) balance(w http.ResponseWriter, r *http.Request) {
	address := r.URL.Query().Get("address")
	if !validAddress(address) {
		writeError(w, http.StatusBadRequest, "invalid address")
		return
	}
	_, balances, _ := s.node.snapshot()
	writeJSON(w, http.StatusOK, map[string]any{"address": address, "balance": balances[address]})
}

func (s *server) newWallet(w http.ResponseWriter, _ *http.Request) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not generate wallet")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"address":    addressFromPublicKey(publicKey),
		"publicKey":  hex.EncodeToString(publicKey),
		"privateKey": hex.EncodeToString(privateKey),
	})
}

func decodeBody(w http.ResponseWriter, r *http.Request, destination any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON request")
		return false
	}
	return true
}

func (s *server) addTransaction(w http.ResponseWriter, r *http.Request) {
	var tx Transaction
	if !decodeBody(w, r, &tx) {
		return
	}
	if err := s.node.addTransaction(tx); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "pending"})
}

func (s *server) mine(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Miner string `json:"miner"`
	}
	if !decodeBody(w, r, &request) {
		return
	}
	block, err := s.node.mine(request.Miner)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"block": block, "reward": blockReward})
}

func main() {
	dataFile := os.Getenv("ROCKY_DATA_FILE")
	if dataFile == "" {
		dataFile = "rocky-testnet.json"
	}
	node, err := NewNode(dataFile)
	if err != nil {
		log.Fatal(err)
	}
	address := os.Getenv("ROCKY_ADDR")
	if address == "" {
		address = "127.0.0.1:8080"
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		log.Fatalf("invalid ROCKY_ADDR: %v", err)
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		log.Fatal(errors.New("this prototype only permits loopback binding"))
	}
	httpServer := &http.Server{
		Addr:              address,
		Handler:           (&server{node: node}).routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("RockyCoin local testnet listening at http://%s", address)
	log.Fatal(httpServer.ListenAndServe())
}
