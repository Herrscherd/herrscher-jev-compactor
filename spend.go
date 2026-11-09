package jev

import (
	"errors"
	"sync"
)

// ErrSpendCapped is returned when a request to Jev would go past the spend cap
// of the compaction. Match it with errors.Is.
var ErrSpendCapped = errors.New("jev spend cap reached")

type spendLedger struct {
	mu          sync.Mutex
	maxRequests int
	maxSent     int
	used        sentUsage
	refused     bool
}

func newSpendLedger(maxRequests, maxSent int) *spendLedger {
	return &spendLedger{maxRequests: maxRequests, maxSent: maxSent}
}

func (s *spendLedger) reserve(tokens int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.overCap(tokens) {
		s.refused = true
		return ErrSpendCapped
	}
	s.used = s.used.plus(sentUsage{requests: 1, tokens: tokens})
	return nil
}

func (s *spendLedger) overCap(tokens int) bool {
	if s.maxRequests > 0 && s.used.requests >= s.maxRequests {
		return true
	}
	return s.maxSent > 0 && s.used.tokens+tokens > s.maxSent
}

func (s *spendLedger) total() sentUsage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.used
}

func (s *spendLedger) capped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refused
}
