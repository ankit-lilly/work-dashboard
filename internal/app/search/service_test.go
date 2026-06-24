package search

import (
	"testing"
	"time"
)

func TestBuildKey(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 6, 1, 0, 0, 0, 0, time.Local)
	end := time.Date(2026, 6, 2, 0, 0, 0, 0, time.Local)
	svc := NewService(nil, nil, nil)

	key := svc.BuildKey(Query{
		Email:        "User@Example.com ",
		Env:          " Prod ",
		StateMachine: " RulesProcessor ",
		StartAt:      &start,
		EndAt:        &end,
	})

	want := "user@example.com|prod|rulesprocessor|2026-06-01|2026-06-02"
	if key != want {
		t.Fatalf("BuildKey returned %q, want %q", key, want)
	}
}

func TestParseDateRange(t *testing.T) {
	t.Parallel()

	svc := NewService(nil, nil, nil)
	start, end, err := svc.ParseDateRange("2026-06-01", "2026-06-02")
	if err != nil {
		t.Fatalf("ParseDateRange returned error: %v", err)
	}
	if start == nil || end == nil {
		t.Fatal("expected both start and end to be set")
	}
	if start.Format("2006-01-02") != "2026-06-01" {
		t.Fatalf("unexpected start date: %v", start)
	}
	if end.Format("2006-01-02") != "2026-06-02" {
		t.Fatalf("unexpected end date: %v", end)
	}
	if end.Hour() != 23 || end.Minute() != 59 {
		t.Fatalf("expected end-of-day bound, got %v", end)
	}
}
