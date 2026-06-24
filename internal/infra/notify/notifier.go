package notify

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/gen2brain/beeep"
)

type Notifier struct{}

func NewNotifier() *Notifier {
	return &Notifier{}
}

func (n *Notifier) Notify(title, body string) error {
	if err := beeep.Notify(title, body, ""); err != nil {
		if fallbackErr := notifyMacHost(title, body); fallbackErr != nil {
			return fmt.Errorf("desktop notification failed: %w (fallback: %v)", err, fallbackErr)
		}
	}
	return nil
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
