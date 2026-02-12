package server

import "strings"

func baseEnvFromKey(envKey string) string {
	if envKey == "" {
		return ""
	}
	if idx := strings.Index(envKey, ":"); idx != -1 {
		return envKey[:idx]
	}
	return envKey
}

func envMatchesSelection(envKey, selection string) bool {
	selection = strings.ToLower(strings.TrimSpace(selection))
	if selection == "" || selection == "all" {
		return true
	}
	envKey = strings.ToLower(strings.TrimSpace(envKey))
	if envKey == selection {
		return true
	}
	return baseEnvFromKey(envKey) == selection
}
