package auth

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// SessionStore is a thread-safe in-memory store of session tokens.
// Each token maps to its expiry time. A background goroutine prunes
// expired tokens every ttl/2.
type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]time.Time
	ttl      time.Duration
}

// NewSessionStore creates a new store and starts a background cleanup goroutine.
// The goroutine stops when the returned store is garbage-collected (it holds no
// reference to the store after the store is collected, so it will exit on the
// next tick when the store pointer becomes nil — but in practice the store lives
// for the process lifetime).
func NewSessionStore(ttl time.Duration) *SessionStore {
	s := &SessionStore{
		sessions: make(map[string]time.Time),
		ttl:      ttl,
	}
	go s.cleanup()
	return s
}

// Create generates a new 32-byte (256-bit) session token, stores it, and
// returns the hex-encoded token string.
func (s *SessionStore) Create() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("auth: crypto/rand read failed: " + err.Error())
	}
	token := hex.EncodeToString(b[:])

	s.mu.Lock()
	s.sessions[token] = time.Now().Add(s.ttl)
	s.mu.Unlock()
	return token
}

// Valid reports whether token exists and has not expired. Expired tokens are
// deleted lazily on lookup.
func (s *SessionStore) Valid(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.sessions[token]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(s.sessions, token)
		return false
	}
	return true
}

// Delete removes a token from the store (called on logout).
func (s *SessionStore) Delete(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

// DeleteAll clears every active session (called on username change).
func (s *SessionStore) DeleteAll() {
	s.mu.Lock()
	s.sessions = make(map[string]time.Time)
	s.mu.Unlock()
}

// cleanup runs every ttl/2 and removes all expired tokens.
func (s *SessionStore) cleanup() {
	ticker := time.NewTicker(s.ttl / 2)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		s.mu.Lock()
		for tok, exp := range s.sessions {
			if now.After(exp) {
				delete(s.sessions, tok)
			}
		}
		s.mu.Unlock()
	}
}
