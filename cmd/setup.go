package cmd

import (
	"fmt"
	"os"

	"github.com/EliLillyCo/work-dashboard/internal/setup"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(newSetupCommand())
}

func newSetupCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Interactive wizard to configure AWS profiles and generate a .env file.",
		Long: `Reads your ~/.aws/config to discover available SSO profiles,
lets you select which ones to monitor, assign environment tiers (dev/qa/prod),
and generates the .env file needed to run the dashboard.`,
		RunE: runSetup,
	}
	cmd.Flags().String("env-file", ".env", "Path to the .env file to generate")
	cmd.Flags().String("aws-config", "", "Path to AWS config file (default: ~/.aws/config)")
	return cmd
}

func runSetup(cmd *cobra.Command, args []string) error {
	envFile, _ := cmd.Flags().GetString("env-file")
	awsConfig, _ := cmd.Flags().GetString("aws-config")

	// Resolve AWS config path.
	if awsConfig == "" {
		defaultPath, err := setup.DefaultConfigPath()
		if err != nil {
			return fmt.Errorf("could not determine AWS config path: %w", err)
		}
		awsConfig = defaultPath
	}

	// Discover profiles.
	fmt.Printf("Reading AWS config from %s...\n", awsConfig)
	profiles, err := setup.DiscoverProfiles(awsConfig)
	if err != nil {
		return err
	}
	if len(profiles) == 0 {
		return fmt.Errorf("no SSO profiles found in %s.\nEnsure your AWS config has profiles with sso_start_url or sso_session.\nRun 'aws configure sso' to set up SSO profiles", awsConfig)
	}
	fmt.Printf("Found %d SSO profile(s).\n\n", len(profiles))

	// Run the interactive wizard.
	result, err := setup.RunWizard(profiles)
	if err != nil {
		// huh returns a specific error on Ctrl+C; handle gracefully.
		fmt.Fprintln(os.Stderr, "\nSetup cancelled.")
		return nil
	}

	if !result.Confirmed {
		fmt.Println("No changes made.")
		return nil
	}

	// Write the .env file.
	envValue := setup.BuildEnvValue(result.Selections)
	cfg := setup.EnvFileConfig{
		JobEnvs: envValue,
		McpEnvs: envValue,
	}

	if err := setup.WriteEnvFile(envFile, cfg); err != nil {
		return fmt.Errorf("write .env file: %w", err)
	}

	fmt.Printf("\n✓ Configuration written to %s\n", envFile)
	fmt.Println("  Run 'radar server' to start the dashboard.")
	return nil
}
