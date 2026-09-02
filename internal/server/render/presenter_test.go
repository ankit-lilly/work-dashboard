package render

import (
	"testing"
	"time"
)

func TestStateMachineDurationAdvancesForRunningExecution(t *testing.T) {
	start := time.Now().Add(-5 * time.Second)
	if got := stateMachineDuration(start, time.Time{}); got == "" {
		t.Fatal("running execution duration should be rendered")
	}
}
