package setup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverProfiles(t *testing.T) {
	// Create a temp AWS config file.
	content := `[default]
region = us-east-1

[profile campdev]
sso_start_url = https://myorg.awsapps.com/start
sso_region = us-east-1
sso_account_id = 111111111111
sso_role_name = DevRole
region = us-east-2

[profile campqa]
sso_session = my-sso
sso_account_id = 222222222222
sso_role_name = QARole
region = us-west-2

[profile campprod]
sso_account_id = 333333333333
region = us-east-2

[profile no-sso-profile]
region = eu-west-1
role_arn = arn:aws:iam::444444444444:role/SomeRole
source_profile = default
`
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config")
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	profiles, err := DiscoverProfiles(configPath)
	if err != nil {
		t.Fatalf("DiscoverProfiles failed: %v", err)
	}

	// Should find 3 SSO profiles (campdev, campprod, campqa), sorted.
	if len(profiles) != 3 {
		t.Fatalf("expected 3 profiles, got %d: %+v", len(profiles), profiles)
	}

	expected := []struct {
		name      string
		region    string
		accountID string
	}{
		{"campdev", "us-east-2", "111111111111"},
		{"campprod", "us-east-2", "333333333333"},
		{"campqa", "us-west-2", "222222222222"},
	}

	for i, exp := range expected {
		if profiles[i].Name != exp.name {
			t.Errorf("profiles[%d].Name = %q, want %q", i, profiles[i].Name, exp.name)
		}
		if profiles[i].Region != exp.region {
			t.Errorf("profiles[%d].Region = %q, want %q", i, profiles[i].Region, exp.region)
		}
		if profiles[i].AccountID != exp.accountID {
			t.Errorf("profiles[%d].AccountID = %q, want %q", i, profiles[i].AccountID, exp.accountID)
		}
	}
}

func TestDiscoverProfiles_NoFile(t *testing.T) {
	_, err := DiscoverProfiles("/nonexistent/path/config")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestDiscoverProfiles_NoSSO(t *testing.T) {
	content := `[default]
region = us-east-1

[profile regular]
region = us-west-2
role_arn = arn:aws:iam::123:role/X
`
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config")
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	profiles, err := DiscoverProfiles(configPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(profiles) != 0 {
		t.Fatalf("expected 0 profiles, got %d", len(profiles))
	}
}

func TestDiscoverProfiles_DefaultIsSSO(t *testing.T) {
	content := `[default]
sso_start_url = https://example.awsapps.com/start
sso_account_id = 999999999999
region = us-east-1
`
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config")
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	profiles, err := DiscoverProfiles(configPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(profiles))
	}
	if profiles[0].Name != "default" {
		t.Errorf("expected profile name 'default', got %q", profiles[0].Name)
	}
}
