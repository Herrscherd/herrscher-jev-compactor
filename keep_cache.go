package jev

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

const keepCacheLimit = 4096

type sessionKey struct {
	name        string
	incarnation string
}

type keepKey struct {
	session sessionKey
	call    string
}

type keepCache struct {
	mu      sync.Mutex
	digests map[keepKey]string
}

func newKeepCache() *keepCache {
	return &keepCache{digests: make(map[keepKey]string)}
}

func (c *keepCache) holds(key keepKey, digest string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	stored, known := c.digests[key]
	return known && stored == digest
}

func (c *keepCache) store(key keepKey, digest string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, known := c.digests[key]; !known && len(c.digests) >= keepCacheLimit {
		clear(c.digests)
	}
	c.digests[key] = digest
}

type keepScope struct {
	cache   *keepCache
	session sessionKey
	goal    string
}

func (c *compactor) scopeFor(session contracts.SessionInfo, goal string) keepScope {
	return keepScope{
		cache:   c.cache,
		session: sessionKey{name: session.Name, incarnation: session.Incarnation},
		goal:    goal,
	}
}

func (s keepScope) key(call toolCall) keepKey {
	return keepKey{session: s.session, call: call.source.ID}
}

func (s keepScope) digest(call toolCall) string {
	fields := []string{s.goal, call.source.Tool, call.source.Input, call.source.Result, call.status()}
	encoded := make([]string, 0, len(fields))
	for _, field := range fields {
		encoded = append(encoded, fmt.Sprintf("%d:%s", len(field), field))
	}
	sum := sha256.Sum256([]byte(strings.Join(encoded, "")))
	return hex.EncodeToString(sum[:])
}

func (s keepScope) partition(calls []toolCall, journal *decisionLog) []toolCall {
	if s.cache == nil {
		return calls
	}
	var unknown []toolCall
	for _, call := range calls {
		if s.cache.holds(s.key(call), s.digest(call)) {
			logDecision(journal, call, decisionKeep, reasonCache)
			continue
		}
		unknown = append(unknown, call)
	}
	return unknown
}

func (s keepScope) remember(kept []toolCall) {
	if s.cache == nil {
		return
	}
	for _, call := range kept {
		s.cache.store(s.key(call), s.digest(call))
	}
}
