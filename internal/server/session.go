package server

import (
	"sync"

	"github.com/google/uuid"
)

// clientSession holds per-client view state managed by the SSE handler goroutine.
// The POST handler communicates changes to the SSE handler via the notify channel.
type clientSession struct {
	mu       sync.Mutex
	smEnv    string // currently selected state machine's environment key
	smArn    string // currently selected state machine's ARN
	smCount  int    // number of executions to show (default 10, grows with "Load More")
	execHash string // content hash of last rendered execution list (for dedup)
	notify   chan struct{}
}

// sessionRegistry manages active client sessions, keyed by a per-connection UUID.
type sessionRegistry struct {
	mu       sync.RWMutex
	sessions map[string]*clientSession
}

func newSessionRegistry() *sessionRegistry {
	return &sessionRegistry{
		sessions: make(map[string]*clientSession),
	}
}

// register creates a new session for the given ID and returns it.
// Called from the SSE handler goroutine on connect.
func (r *sessionRegistry) register(id string) *clientSession {
	sess := &clientSession{
		smCount: 10,
		notify:  make(chan struct{}, 1),
	}
	r.mu.Lock()
	r.sessions[id] = sess
	r.mu.Unlock()
	return sess
}

// unregister removes a session. Called via defer when the SSE connection closes.
func (r *sessionRegistry) unregister(id string) {
	r.mu.Lock()
	delete(r.sessions, id)
	r.mu.Unlock()
}

// get looks up a session by ID. Returns nil if not found.
// Used by the POST handler to signal the SSE handler.
func (r *sessionRegistry) get(id string) *clientSession {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sessions[id]
}

// generateSessionID creates a new unique session identifier.
func generateSessionID() string {
	return uuid.New().String()
}
