package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteEnvFile_NewFile(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")

	cfg := EnvFileConfig{
		JobEnvs: "dev:mydev:us-east-2,prod:myprod:us-east-2",
		McpEnvs: "dev:mydev:us-east-2,prod:myprod:us-east-2",
	}

	if err := WriteEnvFile(envPath, cfg); err != nil {
		t.Fatalf("WriteEnvFile failed: %v", err)
	}

	content, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}

	got := string(content)
	if !strings.Contains(got, "JOB_ENVS=dev:mydev:us-east-2,prod:myprod:us-east-2") {
		t.Errorf("missing JOB_ENVS in output:\n%s", got)
	}
	if !strings.Contains(got, "MCP_ENVS=dev:mydev:us-east-2,prod:myprod:us-east-2") {
		t.Errorf("missing MCP_ENVS in output:\n%s", got)
	}
}

func TestWriteEnvFile_PreservesExisting(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")

	existing := `# My custom settings
JOB_ACTIVE_POLL=10s
JOB_FAILURES_POLL=60s
JOB_ENVS=old:value:us-east-1
MCP_ENVS=old:value:us-east-1
MCP_SECRET_ID_TEMPLATE=some-template-%s
`
	if err := os.WriteFile(envPath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := EnvFileConfig{
		JobEnvs: "dev:newdev:us-east-2",
		McpEnvs: "dev:newdev:us-east-2",
	}

	if err := WriteEnvFile(envPath, cfg); err != nil {
		t.Fatalf("WriteEnvFile failed: %v", err)
	}

	content, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}

	got := string(content)

	// Should preserve other settings.
	if !strings.Contains(got, "# My custom settings") {
		t.Error("comment was not preserved")
	}
	if !strings.Contains(got, "JOB_ACTIVE_POLL=10s") {
		t.Error("JOB_ACTIVE_POLL was not preserved")
	}
	if !strings.Contains(got, "MCP_SECRET_ID_TEMPLATE=some-template-%s") {
		t.Error("MCP_SECRET_ID_TEMPLATE was not preserved")
	}

	// Should NOT contain old values.
	if strings.Contains(got, "old:value:us-east-1") {
		t.Error("old JOB_ENVS/MCP_ENVS value was not removed")
	}

	// Should contain new values.
	if !strings.Contains(got, "JOB_ENVS=dev:newdev:us-east-2") {
		t.Errorf("new JOB_ENVS not found in:\n%s", got)
	}
	if !strings.Contains(got, "MCP_ENVS=dev:newdev:us-east-2") {
		t.Errorf("new MCP_ENVS not found in:\n%s", got)
	}
}

func TestWriteEnvFile_StripsCommentedVariants(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")

	existing := `# JOB_ENVS=commented-out
#MCP_ENVS=also-commented
JOB_ACTIVE_POLL=5s
`
	if err := os.WriteFile(envPath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := EnvFileConfig{
		JobEnvs: "dev:x:us-east-2",
		McpEnvs: "dev:x:us-east-2",
	}

	if err := WriteEnvFile(envPath, cfg); err != nil {
		t.Fatalf("WriteEnvFile failed: %v", err)
	}

	content, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}

	got := string(content)
	if strings.Contains(got, "commented-out") {
		t.Error("commented JOB_ENVS was not stripped")
	}
	if strings.Contains(got, "also-commented") {
		t.Error("commented MCP_ENVS was not stripped")
	}
}

func TestBuildEnvValue(t *testing.T) {
	selections := []Selection{
		{Profile: AWSProfile{Name: "campdev"}, Tier: "dev", Region: "us-east-2"},
		{Profile: AWSProfile{Name: "campqa"}, Tier: "qa", Region: "us-west-2"},
		{Profile: AWSProfile{Name: "campprod"}, Tier: "prod", Region: "us-east-2"},
	}

	got := BuildEnvValue(selections)
	want := "dev:campdev:us-east-2,qa:campqa:us-west-2,prod:campprod:us-east-2"
	if got != want {
		t.Errorf("BuildEnvValue = %q, want %q", got, want)
	}
}

func TestSuggestTier(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"campdev", "dev"},
		{"campqa", "qa"},
		{"campprod", "prod"},
		{"my-staging-profile", "qa"},
		{"sandbox-test", "dev"},
		{"unknown-profile", "dev"},
		{"PROD-account", "prod"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := suggestTier(tt.name)
			if got != tt.want {
				t.Errorf("suggestTier(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}
