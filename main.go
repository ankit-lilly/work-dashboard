package main

import (
	"context"
	"embed"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/EliLillyCo/work-dashboard/internal/aws"
	"github.com/EliLillyCo/work-dashboard/internal/config"
	"github.com/EliLillyCo/work-dashboard/internal/server"
	"log/slog"
	_ "time/tzdata"
)

//go:embed static/* static/**/*
var staticFS embed.FS

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	cfg, err := config.LoadConfig()
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}

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

	awsManager, err := aws.NewClientManager(ctx, cfg)
	if err != nil {
		slog.Error("failed to initialize AWS manager", "err", err)
		os.Exit(1)
	}

	srv := server.NewServer(awsManager, staticFS, cfg)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	httpServer := &http.Server{
		Addr:    ":" + port,
		Handler: srv,
	}

	go func() {
		slog.Info("job viewer starting", "addr", fmt.Sprintf("http://localhost:%s", port))
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
		slog.Error("server forced to shutdown", "err", err)
		os.Exit(1)
	}

	slog.Info("server exited")
}
