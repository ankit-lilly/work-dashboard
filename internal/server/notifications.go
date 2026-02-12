package server

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/EliLillyCo/work-dashboard/internal/aws"
	"github.com/gen2brain/beeep"
)

const (
	notificationMaxItems = 3
	notificationPruneAge = 7 * 24 * time.Hour
)

func (s *Server) notifyNewActiveExecutions(execs []aws.Execution) {
	now := time.Now()

	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()

	if !s.notifyActiveReady {
		for _, exec := range execs {
			if exec.ExecutionArn != "" {
				s.seenActive[exec.ExecutionArn] = now
			}
		}
		s.notifyActiveReady = true
		return
	}

	var newlySeen []aws.Execution
	for _, exec := range execs {
		if exec.ExecutionArn == "" {
			continue
		}
		if _, ok := s.seenActive[exec.ExecutionArn]; !ok {
			newlySeen = append(newlySeen, exec)
		}
		s.seenActive[exec.ExecutionArn] = now
	}

	pruneSeen(s.seenActive, now)

	if len(newlySeen) == 0 {
		return
	}

	title, body := buildActiveNotification(newlySeen)
	if err := beeep.Notify(title, body, ""); err != nil {
		if fallbackErr := notifyMacHost(title, body); fallbackErr != nil {
			log.Printf("desktop notification failed: %v; mac fallback failed: %v", err, fallbackErr)
		}
	}
}

func (s *Server) notifyNewFailures(execs []aws.Execution) {
	now := time.Now()

	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()

	if !s.notifyFailuresReady {
		for _, exec := range execs {
			if exec.ExecutionArn != "" {
				s.seenFailures[exec.ExecutionArn] = now
			}
		}
		s.notifyFailuresReady = true
		return
	}

	var newlySeen []aws.Execution
	for _, exec := range execs {
		if exec.ExecutionArn == "" {
			continue
		}
		if _, ok := s.seenFailures[exec.ExecutionArn]; !ok {
			newlySeen = append(newlySeen, exec)
		}
		s.seenFailures[exec.ExecutionArn] = now
	}

	pruneSeen(s.seenFailures, now)

	if len(newlySeen) == 0 {
		return
	}

	title, body := buildFailureNotification(newlySeen)
	if err := beeep.Notify(title, body, ""); err != nil {
		if fallbackErr := notifyMacHost(title, body); fallbackErr != nil {
			log.Printf("desktop notification failed: %v; mac fallback failed: %v", err, fallbackErr)
		}
	}
}

func pruneSeen(seen map[string]time.Time, now time.Time) {
	cutoff := now.Add(-notificationPruneAge)
	for key, ts := range seen {
		if ts.Before(cutoff) {
			delete(seen, key)
		}
	}
}

func buildActiveNotification(execs []aws.Execution) (string, string) {
	if len(execs) == 1 {
		exec := execs[0]
		title := "New active execution"
		body := formatExecutionLine(exec)
		return title, body
	}

	title := fmt.Sprintf("%d new active executions", len(execs))
	body := formatExecutionList(execs)
	return title, body
}

func buildFailureNotification(execs []aws.Execution) (string, string) {
	if len(execs) == 1 {
		exec := execs[0]
		title := "Execution failed"
		body := formatFailureLine(exec)
		return title, body
	}

	title := fmt.Sprintf("%d new failures", len(execs))
	body := formatFailureList(execs)
	return title, body
}

func formatExecutionLine(exec aws.Execution) string {
	parts := []string{}
	if exec.Env != "" {
		parts = append(parts, exec.Env)
	}
	if exec.Name != "" {
		parts = append(parts, exec.Name)
	}
	if exec.ExecutionName != "" {
		parts = append(parts, exec.ExecutionName)
	}
	return strings.Join(parts, " • ")
}

func formatFailureLine(exec aws.Execution) string {
	parts := []string{}
	if exec.Env != "" {
		parts = append(parts, exec.Env)
	}
	if exec.Name != "" {
		parts = append(parts, exec.Name)
	}
	if exec.ExecutionName != "" {
		parts = append(parts, exec.ExecutionName)
	}
	if exec.ErrorType != "" {
		parts = append(parts, exec.ErrorType)
	}
	return strings.Join(parts, " • ")
}

func formatExecutionList(execs []aws.Execution) string {
	lines := make([]string, 0, minInt(notificationMaxItems, len(execs)))
	for i := 0; i < len(execs) && i < notificationMaxItems; i++ {
		lines = append(lines, formatExecutionLine(execs[i]))
	}
	if len(execs) > notificationMaxItems {
		lines = append(lines, fmt.Sprintf("+%d more", len(execs)-notificationMaxItems))
	}
	return strings.Join(lines, "\n")
}

func formatFailureList(execs []aws.Execution) string {
	lines := make([]string, 0, minInt(notificationMaxItems, len(execs)))
	for i := 0; i < len(execs) && i < notificationMaxItems; i++ {
		lines = append(lines, formatFailureLine(execs[i]))
	}
	if len(execs) > notificationMaxItems {
		lines = append(lines, fmt.Sprintf("+%d more", len(execs)-notificationMaxItems))
	}
	return strings.Join(lines, "\n")
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func notifyMacHost(title, body string) error {
	if title == "" {
		title = "Notification"
	}
	uiURL := strings.TrimSpace(os.Getenv("CAMP_JOB_VIEWER_UI_URL"))

	if _, err := exec.Command("mac", "which", "terminal-notifier").Output(); err == nil {
		args := []string{"terminal-notifier", "-title", title, "-message", body}
		if uiURL != "" {
			args = append(args, "-open", uiURL)
		}
		return exec.Command("mac", args...).Run()
	}

	script := fmt.Sprintf(
		`tell application "System Events" to display notification "%s" with title "%s"`,
		escapeAppleScript(body),
		escapeAppleScript(title),
	)
	return exec.Command("mac", "osascript", "-e", script).Run()
}

func escapeAppleScript(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}
