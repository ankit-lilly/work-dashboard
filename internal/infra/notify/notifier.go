package notify

import (
	"fmt"

	"github.com/gen2brain/beeep"
)

type Notifier struct{}

func NewNotifier() *Notifier {
	return &Notifier{}
}

func (n *Notifier) Notify(title, body string) error {
	if title == "" {
		title = "Notification"
	}
	if err := beeep.Notify(title, body, ""); err != nil {
		return fmt.Errorf("desktop notification failed: %w", err)
	}
	return nil
}
