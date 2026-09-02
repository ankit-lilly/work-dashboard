package server

import (
	"context"
	"sync"

	"github.com/google/uuid"
)

// clientSession holds per-client view state. Command handlers update it and
// wake the SSE execution worker through notify.
type clientSession struct {
	mu         sync.Mutex
	smEnv      string // currently selected state machine's environment key
	smArn      string // currently selected state machine's ARN
	smCount    int    // number of executions to show (default 10, grows with "Load More")
	generation uint64 // increments whenever the requested execution view changes
	execHash   string // content hash of last rendered execution list (for dedup)
	execCancel context.CancelFunc
	statesGen  uint64 // increments whenever the execution-states modal request changes
	statesStop context.CancelFunc
	notify     chan struct{}
}

type sessionView struct {
	env        string
	arn        string
	count      int
	generation uint64
	prevHash   string
}

type statesRequest struct {
	generation uint64
	env        string
	arn        string
	targetID   string
}

func (s *clientSession) view() sessionView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return sessionView{
		env:        s.smEnv,
		arn:        s.smArn,
		count:      s.smCount,
		generation: s.generation,
		prevHash:   s.execHash,
	}
}

func (s *clientSession) selectStateMachine(env, arn string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.smEnv == env && s.smArn == arn {
		return false
	}
	if s.execCancel != nil {
		s.execCancel()
		s.execCancel = nil
	}
	s.smEnv = env
	s.smArn = arn
	s.smCount = 10
	s.generation++
	s.execHash = ""
	return true
}

func (s *clientSession) requestCount(env, arn string, count int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.smEnv != env || s.smArn != arn || arn == "" || count <= s.smCount {
		return false
	}
	if s.execCancel != nil {
		s.execCancel()
		s.execCancel = nil
	}
	s.smCount = count
	s.generation++
	s.execHash = ""
	return true
}

func (s *clientSession) installCancel(generation uint64, cancel context.CancelFunc) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation != generation {
		return false
	}
	s.execCancel = cancel
	return true
}

func (s *clientSession) clearCancel(generation uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation == generation {
		s.execCancel = nil
	}
}

func (s *clientSession) commitHash(view sessionView, hash string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation != view.generation || s.smEnv != view.env || s.smArn != view.arn || s.smCount != view.count {
		return false
	}
	s.execHash = hash
	return true
}

func (s *clientSession) isCurrent(view sessionView) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generation == view.generation && s.smEnv == view.env && s.smArn == view.arn && s.smCount == view.count
}

// beginStatesRequest makes this request the only execution-history stream
// allowed to update this browser session. Starting a newer request cancels the
// older AWS call and its SSE polling loop.
func (s *clientSession) beginStatesRequest(parent context.Context, env, arn, targetID string) (context.Context, statesRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.statesStop != nil {
		s.statesStop()
	}
	s.statesGen++
	ctx, cancel := context.WithCancel(parent)
	s.statesStop = cancel
	return ctx, statesRequest{
		generation: s.statesGen,
		env:        env,
		arn:        arn,
		targetID:   targetID,
	}
}

func (s *clientSession) statesRequestCurrent(request statesRequest) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statesGen == request.generation
}

func (s *clientSession) finishStatesRequest(request statesRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.statesGen == request.generation {
		if s.statesStop != nil {
			s.statesStop()
		}
		s.statesStop = nil
	}
}

func (s *clientSession) cancelStatesRequest() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statesGen++
	if s.statesStop != nil {
		s.statesStop()
		s.statesStop = nil
	}
}

func (s *clientSession) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.execCancel != nil {
		s.execCancel()
		s.execCancel = nil
	}
	if s.statesStop != nil {
		s.statesStop()
		s.statesStop = nil
	}
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
	sess := r.sessions[id]
	delete(r.sessions, id)
	r.mu.Unlock()
	if sess != nil {
		sess.stop()
	}
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
