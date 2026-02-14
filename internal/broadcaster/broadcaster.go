package broadcaster

import (
	"log/slog"
	"sync"
	"time"
)

type Broadcaster[T any] struct {
	clients   map[chan T]struct{}
	mu        sync.RWMutex
	current   T
	currentMu sync.RWMutex
	hasValue  bool
	name      string
}

func NewBroadcaster[T any](fetchFunc func() (T, error), interval time.Duration, name string) *Broadcaster[T] {
	b := &Broadcaster[T]{
		clients: make(map[chan T]struct{}),
		name:    name,
	}
	go b.runFetcher(fetchFunc, interval)
	return b
}

func (b *Broadcaster[T]) runFetcher(fetchFunc func() (T, error), interval time.Duration) {
	slog.Info("broadcaster started", "name", b.name, "interval", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	fetchAndBroadcast := func() {
		data, err := fetchFunc()
		if err != nil {
			return
		}
		b.broadcast(data)
	}

	// Fetch immediately so first subscribers get data without waiting a full interval.
	fetchAndBroadcast()

	for range ticker.C {
		fetchAndBroadcast()
	}
}

func (b *Broadcaster[T]) broadcast(data T) {
	// Update current value
	b.currentMu.Lock()
	b.current = data
	// we set this flag to indicate that we have a valid current value( and not the zero value)
	// we could check the  current variable against the zero value of T but that would not work for all types
	b.hasValue = true
	b.currentMu.Unlock()

	// Send to all clients
	b.mu.RLock()
	defer b.mu.RUnlock()

	for client := range b.clients {
		select {
		case client <- data:
		default:
			// Client channel full, skip
		}
	}
}

func (b *Broadcaster[T]) Subscribe() chan T {
	ch := make(chan T, 5)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()

	// Send current value immediately if available
	b.currentMu.RLock()
	if b.hasValue {
		select {
		case ch <- b.current:
		default:
		}
	}
	b.currentMu.RUnlock()

	return ch
}

func (b *Broadcaster[T]) Unsubscribe(ch chan T) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, exists := b.clients[ch]; !exists {
		return // Already unsubscribed
	}

	delete(b.clients, ch)
	close(ch)
}

func (b *Broadcaster[T]) ClientCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.clients)
}
