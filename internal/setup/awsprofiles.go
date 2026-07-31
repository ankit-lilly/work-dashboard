package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"gopkg.in/ini.v1"
)

// AWSProfile represents a discovered SSO profile from ~/.aws/config.
type AWSProfile struct {
	Name      string // profile name (the part after [profile ...])
	Region    string // region value (used as default for the wizard)
	SSORegion string // sso_region value
	AccountID string // sso_account_id
	RoleName  string // sso_role_name
}

// DefaultConfigPath returns the resolved path to ~/.aws/config.
func DefaultConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".aws", "config"), nil
}

// DiscoverProfiles parses the AWS config file and returns all SSO profiles.
// A profile is considered SSO if it has sso_start_url, sso_session, or sso_account_id keys.
func DiscoverProfiles(configPath string) ([]AWSProfile, error) {
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("AWS config file not found at %s.\nRun 'aws configure sso' to set up SSO profiles first", configPath)
	}

	cfg, err := ini.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("parse AWS config file: %w", err)
	}

	var profiles []AWSProfile

	for _, section := range cfg.Sections() {
		name := section.Name()

		// AWS config uses [profile X] for named profiles and [default] for the default.
		var profileName string
		if cut, ok := strings.CutPrefix(name, "profile "); ok {
			profileName = cut
		} else if name == "default" {
			profileName = "default"
		} else {
			continue
		}

		// Check if this is an SSO profile.
		if !isSSO(section) {
			continue
		}

		profiles = append(profiles, AWSProfile{
			Name:      profileName,
			Region:    section.Key("region").String(),
			SSORegion: section.Key("sso_region").String(),
			AccountID: section.Key("sso_account_id").String(),
			RoleName:  section.Key("sso_role_name").String(),
		})
	}

	sort.Slice(profiles, func(i, j int) bool {
		return profiles[i].Name < profiles[j].Name
	})

	return profiles, nil
}

// isSSO checks whether an INI section represents an SSO profile.
func isSSO(section *ini.Section) bool {
	ssoKeys := []string{"sso_start_url", "sso_session", "sso_account_id"}
	return slices.ContainsFunc(ssoKeys, func(key string) bool {
		return section.HasKey(key)
	})
}
