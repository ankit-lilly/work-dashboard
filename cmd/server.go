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

	app_execution "github.com/EliLillyCo/work-dashboard/internal/app/execution"
	app_lambda "github.com/EliLillyCo/work-dashboard/internal/app/lambda"
	app_notification "github.com/EliLillyCo/work-dashboard/internal/app/notification"
	app_rds "github.com/EliLillyCo/work-dashboard/internal/app/rds"
	app_search "github.com/EliLillyCo/work-dashboard/internal/app/search"
	"github.com/EliLillyCo/work-dashboard/internal/config"
	"github.com/EliLillyCo/work-dashboard/internal/infra/awsclient"
	infra_jokes "github.com/EliLillyCo/work-dashboard/internal/infra/jokes"
	infra_lambda "github.com/EliLillyCo/work-dashboard/internal/infra/lambda"
	infra_notify "github.com/EliLillyCo/work-dashboard/internal/infra/notify"
	infra_rds "github.com/EliLillyCo/work-dashboard/internal/infra/rds"
	infra_s3 "github.com/EliLillyCo/work-dashboard/internal/infra/s3"
	infra_sfn "github.com/EliLillyCo/work-dashboard/internal/infra/sfn"
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

			awsManager, err := awsclient.NewClientManager(ctx, cfg)
			if err != nil {
				return fmt.Errorf("initialize AWS manager: %w", err)
			}

			addr, _ := cmd.Flags().GetString("addr")

			execRepos := make(map[string]app_execution.ExecutionRepository, len(awsManager.Clients))
			lambdaExecRepos := make(map[string]app_lambda.ExecutionRepository, len(awsManager.Clients))
			lambdaRepos := make(map[string]app_lambda.LambdaRepository, len(awsManager.Clients))
			rdsRepos := make(map[string]app_rds.RDSRepository, len(awsManager.Clients))
			searchExecRepos := make(map[string]app_search.ExecutionSearchRepository, len(awsManager.Clients))
			searchObjectRepos := make(map[string]app_search.ObjectSearchRepository, len(awsManager.Clients))

			for env, client := range awsManager.Clients {
				sfnRepo := infra_sfn.NewRepository(client)
				execRepos[env] = sfnRepo
				lambdaExecRepos[env] = sfnRepo
				searchExecRepos[env] = sfnRepo
				lambdaRepos[env] = infra_lambda.NewRepository(client)
				rdsRepos[env] = infra_rds.NewRepository(client)
				searchObjectRepos[env] = infra_s3.NewRepository(client)
			}

			notifyService := app_notification.NewService(infra_notify.NewNotifier())
			execService := app_execution.NewService(execRepos, cfg, notifyService)
			lambdaService := app_lambda.NewService(lambdaExecRepos, lambdaRepos, execService.ActiveCount)
			rdsService := app_rds.NewService(rdsRepos, cfg, execService.ActiveCount)
			searchService := app_search.NewService(searchExecRepos, searchObjectRepos, cfg)
			jokeProvider := infra_jokes.NewClient()

			srv, err := server.NewServer(execService, lambdaService, rdsService, searchService, cfg, staticFS, jokeProvider)
			if err != nil {
				return fmt.Errorf("initialize server: %w", err)
			}
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
