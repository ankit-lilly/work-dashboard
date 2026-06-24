package notification

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	domain_execution "github.com/EliLillyCo/work-dashboard/internal/domain/execution"
)

const (
	maxItems = 3
	pruneAge = 7 * 24 * time.Hour
)

type Notifier interface {
	Notify(title, body string) error
}

type Service struct {
	notifier Notifier

	mu            sync.Mutex
	activeReady   bool
	failuresReady bool
	seenActive    map[string]time.Time
	seenFailures  map[string]time.Time
}

func NewService(notifier Notifier) *Service {
	return &Service{
		notifier:     notifier,
		seenActive:   make(map[string]time.Time),
		seenFailures: make(map[string]time.Time),
	}
}

func (s *Service) ObserveActive(execs []domain_execution.Summary) {
	s.observe(execs, true)
}

func (s *Service) ObserveFailures(execs []domain_execution.Summary) {
	s.observe(execs, false)
}

func (s *Service) observe(execs []domain_execution.Summary, active bool) {
	if s.notifier == nil {
		return
	}
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	ready := &s.failuresReady
	seen := s.seenFailures
	if active {
		ready = &s.activeReady
		seen = s.seenActive
	}

	if !*ready {
		for _, exec := range execs {
			if exec.ExecutionArn != "" {
				seen[exec.ExecutionArn] = now
			}
		}
		*ready = true
		return
	}

	var newlySeen []domain_execution.Summary
	for _, exec := range execs {
		if exec.ExecutionArn == "" {
			continue
		}
		if _, ok := seen[exec.ExecutionArn]; !ok {
			newlySeen = append(newlySeen, exec)
		}
		seen[exec.ExecutionArn] = now
	}

	pruneSeen(seen, now)
	if len(newlySeen) == 0 {
		return
	}

	title, body := buildNotification(newlySeen, active)
	if err := s.notifier.Notify(title, body); err != nil {
		slog.Warn("desktop notification failed", "err", err)
	}
}

func pruneSeen(seen map[string]time.Time, now time.Time) {
	cutoff := now.Add(-pruneAge)
	for key, ts := range seen {
		if ts.Before(cutoff) {
			delete(seen, key)
		}
	}
}

func buildNotification(execs []domain_execution.Summary, active bool) (string, string) {
	if active {
		if len(execs) == 1 {
			return "New active execution", formatExecutionLine(execs[0], false)
		}
		return fmt.Sprintf("%d new active executions", len(execs)), formatExecutionList(execs, false)
	}

	if len(execs) == 1 {
		return "Execution failed", formatExecutionLine(execs[0], true)
	}
	return fmt.Sprintf("%d new failures", len(execs)), formatExecutionList(execs, true)
}

func formatExecutionLine(exec domain_execution.Summary, includeError bool) string {
	parts := make([]string, 0, 4)
	if exec.Env != "" {
		parts = append(parts, exec.Env)
	}
	if exec.Name != "" {
		parts = append(parts, exec.Name)
	}
	if exec.ExecutionName != "" {
		parts = append(parts, exec.ExecutionName)
	}
	if includeError && exec.ErrorType != "" {
		parts = append(parts, exec.ErrorType)
	}
	return strings.Join(parts, " • ")
}

func formatExecutionList(execs []domain_execution.Summary, includeError bool) string {
	lines := make([]string, 0, min(maxItems, len(execs)))
	for i := 0; i < len(execs) && i < maxItems; i++ {
		lines = append(lines, formatExecutionLine(execs[i], includeError))
	}
	if len(execs) > maxItems {
		lines = append(lines, fmt.Sprintf("+%d more", len(execs)-maxItems))
	}
	return strings.Join(lines, "\n")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
