package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/teilomillet/hapax/errors"
	"github.com/teilomillet/hapax/server"
	"go.uber.org/zap"
)

func main() {
	// Create logger with explicit error handling
	logger, err := zap.NewProduction()
	if err != nil {
		// Fail fast if logger creation fails
		fmt.Printf("Critical error: Failed to create logger: %v\n", err)
		os.Exit(1)
	}

	// Ensure logger is synced, with robust error handling
	defer func() {
		if syncErr := logger.Sync(); syncErr != nil {
			// Log sync failure, but don't mask the original error
			fmt.Printf("Warning: Failed to sync logger: %v\n", syncErr)
		}
	}()

	// Set global logger
	errors.SetLogger(logger)

	// Configuration and server setup with comprehensive error handling
	configPath := "config.yaml"
	server, err := server.NewServer(configPath, logger)
	if err != nil {
		logger.Fatal("Server initialization failed",
			zap.Error(err),
			zap.String("config_path", configPath),
		)
	}

	// Graceful shutdown infrastructure
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Signal handling with detailed logging
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		logger.Info("Shutdown signal received",
			zap.String("signal", sig.String()),
			zap.String("action", "initiating graceful shutdown"),
		)
		cancel()
	}()

	// Server start with comprehensive error tracking
	if err := server.Start(ctx); err != nil {
		logger.Fatal("Server startup or runtime error",
			zap.Error(err),
			zap.String("action", "server_start_failed"),
		)
	}
}
