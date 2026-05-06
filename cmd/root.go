package cmd

import (
	"fmt"
	"io/fs"
	"os"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
)

var (
	envFilePath string
	version     = "dev"
	staticFS    fs.FS
	schemaSQL   string
)

func SetVersion(v string)   { version = v }
func SetStaticFS(f fs.FS)   { staticFS = f }
func SetSchemaSQL(s string) { schemaSQL = s }

var rootCmd = &cobra.Command{
	Use:           "work-dashboard",
	Short:         "CAMP Job Viewer and MCP Investigator.",
	SilenceUsage:  true,
	SilenceErrors: true,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.Version = version
	rootCmd.SetVersionTemplate("{{.Version}}\n")

	rootCmd.PersistentFlags().StringVar(
		&envFilePath,
		"env-file",
		"",
		"Path to a .env file to load before executing.",
	)

	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if envFilePath != "" {
			return godotenv.Load(envFilePath)
		}
		_ = godotenv.Load()
		return nil
	}
}
