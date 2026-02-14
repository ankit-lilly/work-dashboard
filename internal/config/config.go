package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type EnvMapping struct {
	Name    string
	BaseEnv string
	Profile string
	Region  string
}

type Config struct {
	Envs    []EnvMapping
	Polling PollingConfig
	Limits  LimitsConfig
}

type PollingConfig struct {
	ActiveInterval        time.Duration
	FailuresInterval      time.Duration
	StateMachinesInterval time.Duration
	ActiveIntervalByEnv   map[string]time.Duration
}

type LimitsConfig struct {
	RequestTimeout      time.Duration
	MaxPreviewFileSize  int64
	MaxJSONLineSize     int
	JSONPreviewMaxChars int
	SearchStateTTL      time.Duration
}

func LoadConfig() (*Config, error) {
	_ = godotenv.Load() // Ignore error if .env doesn't exist

	envs := os.Getenv("JOB_ENVS")
	if envs == "" {
		// Default to a sane developer default if nothing is provided
		envs = "dev:campdev:us-east-2,qa:campqa:us-east-2,prod:campprod:us-east-2,dev:dsoadev:us-east-2,qa:dsoaqa:us-east-2"
	}

	config := &Config{
		Polling: defaultPollingConfig(),
		Limits:  defaultLimitsConfig(),
	}
	for part := range strings.SplitSeq(envs, ",") {
		mapping := strings.Split(part, ":")
		if len(mapping) < 2 {
			return nil, fmt.Errorf("invalid env mapping format: %s. Expected name:profile[:region]", part)
		}

		region := "us-east-2"
		if len(mapping) == 3 {
			region = mapping[2]
		}

		baseEnv := mapping[0]
		profile := mapping[1]
		key := baseEnv + ":" + profile
		config.Envs = append(config.Envs, EnvMapping{
			Name:    key,
			BaseEnv: baseEnv,
			Profile: profile,
			Region:  region,
		})
	}

	applyPollingEnv(config)
	applyLimitsEnv(config)
	return config, nil
}

func defaultPollingConfig() PollingConfig {
	return PollingConfig{
		ActiveInterval:        5 * time.Second,
		FailuresInterval:      120 * time.Second,
		StateMachinesInterval: 5 * time.Minute,
		ActiveIntervalByEnv: map[string]time.Duration{
			"dev":  10 * time.Second,
			"qa":   10 * time.Second,
			"prod": 30 * time.Second,
		},
	}
}

func defaultLimitsConfig() LimitsConfig {
	return LimitsConfig{
		RequestTimeout:      60 * time.Second,
		MaxPreviewFileSize:  2 * 1024 * 1024, // 2MB
		MaxJSONLineSize:     2 * 1024 * 1024, // 2MB
		JSONPreviewMaxChars: 300,
		SearchStateTTL:      10 * time.Minute,
	}
}

func applyPollingEnv(cfg *Config) {
	cfg.Polling.ActiveInterval = parseDurationEnv("JOB_ACTIVE_POLL", cfg.Polling.ActiveInterval)
	cfg.Polling.FailuresInterval = parseDurationEnv("JOB_FAILURES_POLL", cfg.Polling.FailuresInterval)
	cfg.Polling.StateMachinesInterval = parseDurationEnv("JOB_STATE_MACHINES_POLL", cfg.Polling.StateMachinesInterval)

	cacheEnv := strings.TrimSpace(os.Getenv("JOB_ACTIVE_POLL_BY_ENV"))
	if cacheEnv == "" {
		return
	}
	overrides := make(map[string]time.Duration, len(cfg.Polling.ActiveIntervalByEnv))
	for k, v := range cfg.Polling.ActiveIntervalByEnv {
		overrides[strings.ToLower(k)] = v
	}
	for part := range strings.SplitSeq(cacheEnv, ",") {
		kv := strings.Split(part, "=")
		if len(kv) != 2 {
			slog.Warn("invalid cache config entry, skipping", "entry", part)
			continue
		}
		env := strings.ToLower(strings.TrimSpace(kv[0]))
		dur, err := time.ParseDuration(strings.TrimSpace(kv[1]))
		if err != nil {
			slog.Warn("invalid cache duration", "env", env, "value", kv[1], "error", err)
			continue
		}
		if env == "" {
			slog.Warn("empty environment name in cache config", "entry", part)
			continue
		}
		overrides[env] = dur
	}
	cfg.Polling.ActiveIntervalByEnv = overrides
}

func parseDurationEnv(key string, fallback time.Duration) time.Duration {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return fallback
	}
	d, err := time.ParseDuration(val)
	if err != nil {
		return fallback
	}
	return d
}

func applyLimitsEnv(cfg *Config) {
	cfg.Limits.RequestTimeout = parseDurationEnv("REQUEST_TIMEOUT", cfg.Limits.RequestTimeout)
	cfg.Limits.SearchStateTTL = parseDurationEnv("SEARCH_STATE_TTL", cfg.Limits.SearchStateTTL)

	if val := strings.TrimSpace(os.Getenv("MAX_PREVIEW_FILE_SIZE")); val != "" {
		var size int64
		if _, err := fmt.Sscanf(val, "%d", &size); err == nil && size > 0 {
			cfg.Limits.MaxPreviewFileSize = size
		}
	}

	if val := strings.TrimSpace(os.Getenv("MAX_JSON_LINE_SIZE")); val != "" {
		var size int
		if _, err := fmt.Sscanf(val, "%d", &size); err == nil && size > 0 {
			cfg.Limits.MaxJSONLineSize = size
		}
	}

	if val := strings.TrimSpace(os.Getenv("JSON_PREVIEW_MAX_CHARS")); val != "" {
		var chars int
		if _, err := fmt.Sscanf(val, "%d", &chars); err == nil && chars > 0 {
			cfg.Limits.JSONPreviewMaxChars = chars
		}
	}
}
