package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/EliLillyCo/work-dashboard/internal/aws"
	"github.com/EliLillyCo/work-dashboard/internal/config"
	"github.com/EliLillyCo/work-dashboard/internal/server"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(newServerCommand())
}

func newServerCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "server",
		Short:         "Start the CAMP dashboard HTTP server.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
				Level: slog.LevelInfo,
			}))
			slog.SetDefault(logger)

			if tz := os.Getenv("JOB_TZ"); tz != "" {
				if loc, err := time.LoadLocation(tz); err != nil {
					slog.Warn("invalid JOB_TZ, falling back to default", "tz", tz, "err", err)
				} else {
					time.Local = loc
				}
			} else if tz := os.Getenv("TZ"); tz != "" {
				if loc, err := time.LoadLocation(tz); err != nil {
					slog.Warn("invalid TZ, falling back to default", "tz", tz, "err", err)
				} else {
					time.Local = loc
				}
			}

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			cfg, err := config.LoadConfig()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			awsManager, err := aws.NewClientManager(ctx, cfg)
			if err != nil {
				return fmt.Errorf("initialize AWS manager: %w", err)
			}

			addr, _ := cmd.Flags().GetString("addr")

			srv := server.NewServer(awsManager, staticFS, cfg)
			httpServer := &http.Server{
				Addr:    addr,
				Handler: srv,
			}

			go func() {
				slog.Info("job viewer starting", "addr", fmt.Sprintf("http://localhost%s", addr))
				if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					slog.Error("listen and serve error", "err", err)
					os.Exit(1)
				}
			}()

			<-ctx.Done()
			slog.Info("shutting down gracefully")

			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if err := httpServer.Shutdown(shutdownCtx); err != nil {
				return fmt.Errorf("server forced to shutdown: %w", err)
			}

			slog.Info("server exited")
			return nil
		},
	}

	cmd.Flags().String("addr", ":8080", "Address to bind the HTTP server to.")

	return cmd
}
