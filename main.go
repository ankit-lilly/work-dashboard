package main

import (
	"context"
	"embed"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/EliLillyCo/work-dashboard/internal/aws"
	"github.com/EliLillyCo/work-dashboard/internal/config"
	"github.com/EliLillyCo/work-dashboard/internal/server"
)

//go:embed static/* static/**/*
var staticFS embed.FS

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	awsManager, err := aws.NewClientManager(ctx, cfg)
	if err != nil {
		log.Fatalf("Failed to initialize AWS manager: %v", err)
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
		fmt.Printf("Job Viewer starting on http://localhost:%s\n", port)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Listen and serve error: %v", err)
		}
	}()

	<-ctx.Done()
	fmt.Println("\nShutting down gracefully...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	fmt.Println("Server exited.")
}
