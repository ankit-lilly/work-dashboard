package statemachine

import "testing"

func TestBaseEnvFromKey(t *testing.T) {
	t.Parallel()

	if got := BaseEnvFromKey("prod:campprod"); got != "prod" {
		t.Fatalf("BaseEnvFromKey returned %q, want %q", got, "prod")
	}
	if got := BaseEnvFromKey("qa"); got != "qa" {
		t.Fatalf("BaseEnvFromKey returned %q, want %q", got, "qa")
	}
}

func TestEnvMatchesSelection(t *testing.T) {
	t.Parallel()

	if !EnvMatchesSelection("prod:campprod", "prod") {
		t.Fatal("expected base environment selection to match")
	}
	if !EnvMatchesSelection("prod:campprod", "prod:campprod") {
		t.Fatal("expected full environment selection to match")
	}
	if EnvMatchesSelection("qa:campqa", "prod") {
		t.Fatal("did not expect mismatched environment to match")
	}
}

func TestIncludeNameWithConfiguredPrefixes(t *testing.T) {
	t.Parallel()

	if !IncludeName("custom-prefix-bus-rules", []string{"custom-prefix"}) {
		t.Fatal("expected configured prefix to match")
	}
	if IncludeName("other-prefix-bus-rules", []string{"custom-prefix"}) {
		t.Fatal("did not expect non-matching configured prefix to match")
	}
}
