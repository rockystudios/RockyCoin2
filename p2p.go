package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	maxPeers         = 32
	maxChainResponse = 32 << 20
	syncInterval     = 10 * time.Second
)

type peerSet struct {
	mu     sync.Mutex
	urls   map[string]struct{}
	client *http.Client
}

func newPeerSet() *peerSet {
	return &peerSet{
		urls: make(map[string]struct{}),
		client: &http.Client{
			Timeout: 5 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func isLoopbackHost(host string) bool {
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// normalizePeer restricts peers to plain loopback HTTP origins so the node cannot be used to reach other hosts.
func normalizePeer(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("peer must be a plain http://host:port URL")
	}
	if !isLoopbackHost(parsed.Hostname()) || parsed.Port() == "" {
		return "", errors.New("this prototype only permits loopback peers with an explicit port")
	}
	return "http://" + parsed.Host, nil
}

func (p *peerSet) add(raw string, self string) (string, error) {
	peer, err := normalizePeer(raw)
	if err != nil {
		return "", err
	}
	if peer == self {
		return "", errors.New("a node cannot peer with itself")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.urls[peer]; !exists && len(p.urls) >= maxPeers {
		return "", errors.New("peer limit reached")
	}
	p.urls[peer] = struct{}{}
	return peer, nil
}

func (p *peerSet) list() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	peers := make([]string, 0, len(p.urls))
	for peer := range p.urls {
		peers = append(peers, peer)
	}
	sort.Strings(peers)
	return peers
}

func (p *peerSet) post(peer, path string, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	response, err := p.client.Post(peer+path, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64*1024))
	if response.StatusCode >= 300 {
		return fmt.Errorf("peer %s returned %s", peer, response.Status)
	}
	return nil
}

func (p *peerSet) broadcast(path string, value any) {
	for _, peer := range p.list() {
		go func(peer string) { _ = p.post(peer, path, value) }(peer)
	}
}

func (p *peerSet) fetchChain(peer string) ([]Block, error) {
	response, err := p.client.Get(peer + "/api/chain")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("peer %s returned %s", peer, response.Status)
	}
	var payload struct {
		Chain []Block `json:"chain"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxChainResponse)).Decode(&payload); err != nil {
		return nil, err
	}
	return payload.Chain, nil
}

// syncOnce asks every peer for its chain and adopts the longest valid one. It reports whether the chain changed.
func (s *server) syncOnce() bool {
	changed := false
	for _, peer := range s.peers.list() {
		chain, err := s.peers.fetchChain(peer)
		if err != nil {
			continue
		}
		if replaced, err := s.node.replaceChain(chain); err == nil && replaced {
			changed = true
		}
	}
	return changed
}

func (s *server) syncLoop() {
	ticker := time.NewTicker(syncInterval)
	defer ticker.Stop()
	for range ticker.C {
		s.syncOnce()
	}
}
