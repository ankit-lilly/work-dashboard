package cmd

import (
	"log/slog"

	mcptools "github.com/EliLillyCo/work-dashboard/internal/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(newMcpCommand())
}

func newMcpCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "mcp",
		Short:         "Start the CAMP Investigator MCP server over stdio.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			pool := mcptools.BuildPool()
			defer pool.Close()

			s := mcptools.NewMCPServer(pool, schemaSQL)

			slog.Info("camp-mcp starting on stdio")
			return mcpserver.ServeStdio(s)
		},
	}

	return cmd
}
