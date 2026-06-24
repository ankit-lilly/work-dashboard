package statemachine

import "strings"

type StateMachine struct {
	Env     string
	BaseEnv string
	Name    string
	Arn     string
}

func IncludeName(name string, prefixes []string) bool {
	if name == "" {
		return false
	}
	if len(prefixes) > 0 {
		for _, prefix := range prefixes {
			if strings.HasPrefix(name, prefix) {
				return true
			}
		}
		return false
	}
	if !strings.HasPrefix(name, "mdids-account-automation-relv305-backend") &&
		!strings.HasPrefix(name, "mdids-account-automation-backend") &&
		!strings.HasPrefix(name, "atom5-qa-stack-") &&
		!strings.HasPrefix(name, "lrl-dtf-sdr") {
		return false
	}
	if strings.HasPrefix(name, "lrl-dtf-sdr") {
		return true
	}
	suffixes := []string{
		"BusRulesProcessor",
		"ATOM5RulesProcessor",
		"VeevaRulesProcessor",
		"IWRSRulesProcessor",
		"IWRSDataProcessor",
		"VeevaDataProcessor",
		"Atom5DataProcessor",
		"Infohub",
	}
	for _, suffix := range suffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

func IsRulesProcessor(name string) bool {
	return strings.Contains(name, "RulesProcessor")
}

func BaseEnvFromKey(envKey string) string {
	if envKey == "" {
		return ""
	}
	if idx := strings.Index(envKey, ":"); idx != -1 {
		return envKey[:idx]
	}
	return envKey
}

func EnvMatchesSelection(envKey, selection string) bool {
	selection = strings.ToLower(strings.TrimSpace(selection))
	if selection == "" || selection == "all" {
		return true
	}
	envKey = strings.ToLower(strings.TrimSpace(envKey))
	if envKey == selection {
		return true
	}
	return BaseEnvFromKey(envKey) == selection
}
