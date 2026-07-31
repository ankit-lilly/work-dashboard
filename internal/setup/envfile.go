package setup

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnvFileConfig holds the generated env vars to write.
type EnvFileConfig struct {
	JobEnvs string
	McpEnvs string
}

// WriteEnvFile writes or updates the .env file at the given path.
// If the file already exists, it preserves all lines except JOB_ENVS and MCP_ENVS,
// then appends the new values. If it doesn't exist, creates a fresh file.
func WriteEnvFile(path string, cfg EnvFileConfig) error {
	preserved, err := readExistingEnv(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read existing .env: %w", err)
	}

	var lines []string
	lines = append(lines, preserved...)

	// Remove trailing blank lines to keep output tidy.
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}

	// Add a blank separator if there's existing content.
	if len(lines) > 0 {
		lines = append(lines, "")
	}

	lines = append(lines, fmt.Sprintf("JOB_ENVS=%s", cfg.JobEnvs))
	lines = append(lines, fmt.Sprintf("MCP_ENVS=%s", cfg.McpEnvs))
	lines = append(lines, "") // trailing newline

	content := strings.Join(lines, "\n")

	// Atomic write: write to temp file in same dir, then rename.
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".env.tmp.*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename temp to %s: %w", path, err)
	}

	return nil
}

// readExistingEnv reads an existing .env file and returns lines that are NOT
// JOB_ENVS or MCP_ENVS (preserving comments, blank lines, other settings).
func readExistingEnv(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var preserved []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Skip lines that set JOB_ENVS or MCP_ENVS (we'll replace them).
		if strings.HasPrefix(trimmed, "JOB_ENVS=") || strings.HasPrefix(trimmed, "MCP_ENVS=") {
			continue
		}
		// Also skip commented-out versions.
		if strings.HasPrefix(trimmed, "#JOB_ENVS=") || strings.HasPrefix(trimmed, "#MCP_ENVS=") ||
			strings.HasPrefix(trimmed, "# JOB_ENVS=") || strings.HasPrefix(trimmed, "# MCP_ENVS=") {
			continue
		}

		preserved = append(preserved, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan .env file: %w", err)
	}

	return preserved, nil
}
