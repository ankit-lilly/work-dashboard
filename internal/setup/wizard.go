package setup

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"
)

// Selection represents a user's choice for one profile.
type Selection struct {
	Profile AWSProfile
	Tier    string // dev, qa, prod, or custom
	Region  string // AWS region
}

// WizardResult holds all user choices from the interactive wizard.
type WizardResult struct {
	Selections []Selection
	Confirmed  bool
}

// RunWizard presents the interactive multi-step wizard.
// Returns the user's selections or an error if cancelled/interrupted.
func RunWizard(profiles []AWSProfile) (*WizardResult, error) {
	if len(profiles) == 0 {
		return nil, fmt.Errorf("no profiles to configure")
	}

	// Step 1: Multi-select profiles.
	options := make([]huh.Option[int], len(profiles))
	for i, p := range profiles {
		label := p.Name
		if p.AccountID != "" {
			label = fmt.Sprintf("%s (%s)", p.Name, p.AccountID)
		}
		options[i] = huh.NewOption(label, i)
	}

	var selectedIndices []int
	selectForm := huh.NewForm(
		huh.NewGroup(
			huh.NewMultiSelect[int]().
				Title("Select AWS SSO profiles to monitor").
				Description("Use space to select, enter to confirm").
				Options(options...).
				Value(&selectedIndices).
				Validate(func(vals []int) error {
					if len(vals) == 0 {
						return fmt.Errorf("select at least one profile")
					}
					return nil
				}),
		),
	)
	if err := selectForm.Run(); err != nil {
		return nil, err
	}

	// Step 2: For each selected profile, assign tier and region.
	selections := make([]Selection, 0, len(selectedIndices))
	for _, idx := range selectedIndices {
		p := profiles[idx]

		tier := suggestTier(p.Name)
		region := p.Region
		if region == "" {
			region = "us-east-2"
		}

		tierForm := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title(fmt.Sprintf("Environment tier for '%s'", p.Name)).
					Options(
						huh.NewOption("dev", "dev"),
						huh.NewOption("qa", "qa"),
						huh.NewOption("prod", "prod"),
						huh.NewOption("custom...", "custom"),
					).
					Value(&tier),
				huh.NewInput().
					Title("AWS Region").
					Value(&region),
			),
		)
		if err := tierForm.Run(); err != nil {
			return nil, err
		}

		// Handle custom tier.
		if tier == "custom" {
			customTier := ""
			customForm := huh.NewForm(
				huh.NewGroup(
					huh.NewInput().
						Title("Enter custom tier name").
						Value(&customTier).
						Validate(func(s string) error {
							if strings.TrimSpace(s) == "" {
								return fmt.Errorf("tier name cannot be empty")
							}
							return nil
						}),
				),
			)
			if err := customForm.Run(); err != nil {
				return nil, err
			}
			tier = strings.TrimSpace(customTier)
		}

		selections = append(selections, Selection{
			Profile: p,
			Tier:    tier,
			Region:  region,
		})
	}

	// Step 3: Preview and confirm.
	envValue := BuildEnvValue(selections)
	preview := fmt.Sprintf("JOB_ENVS=%s\nMCP_ENVS=%s", envValue, envValue)

	var confirmed bool
	confirmForm := huh.NewForm(
		huh.NewGroup(
			huh.NewNote().
				Title("Configuration Preview").
				Description(preview),
			huh.NewConfirm().
				Title("Write this configuration to .env?").
				Affirmative("Yes, write it").
				Negative("No, cancel").
				Value(&confirmed),
		),
	)
	if err := confirmForm.Run(); err != nil {
		return nil, err
	}

	return &WizardResult{Selections: selections, Confirmed: confirmed}, nil
}

// BuildEnvValue formats selections into the JOB_ENVS string format:
// "tier:profile:region,tier:profile:region,..."
func BuildEnvValue(selections []Selection) string {
	parts := make([]string, len(selections))
	for i, s := range selections {
		parts[i] = fmt.Sprintf("%s:%s:%s", s.Tier, s.Profile.Name, s.Region)
	}
	return strings.Join(parts, ",")
}

// suggestTier attempts to guess the tier from a profile name.
func suggestTier(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "prod"):
		return "prod"
	case strings.Contains(lower, "qa") || strings.Contains(lower, "staging") || strings.Contains(lower, "stage"):
		return "qa"
	case strings.Contains(lower, "dev") || strings.Contains(lower, "sandbox"):
		return "dev"
	default:
		return "dev"
	}
}
