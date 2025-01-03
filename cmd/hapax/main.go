package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/teilomillet/hapax/config"
	"github.com/teilomillet/hapax/server"
	"go.uber.org/zap"
)

var (
	configFile = flag.String("config", "hapax.yaml", "Path to configuration file")
	validate   = flag.Bool("validate", false, "Validate configuration and exit")
	version    = flag.Bool("version", false, "Print version and exit")
)

const Version = "v0.0.25"

func main() {
	flag.Parse()

	if *version {
		fmt.Printf("hapax %s\n", Version)
		os.Exit(0)
	}

	// Create logger
	logger, err := zap.NewProduction()
	if err != nil {
		log.Fatalf("Failed to create logger: %v", err)
	}
	defer func() {
		if err := logger.Sync(); err != nil {
			// Log sync failure, but use fmt.Fprintf to stderr since the zap logger might be unavailable
			fmt.Fprintf(os.Stderr, "Failed to sync logger: %v\n", err)
		}
	}()

	// Load configuration
	cfg, err := config.LoadFile(*configFile)
	if err != nil {
		logger.Fatal("Failed to load config",
			zap.Error(err),
			zap.String("config_file", *configFile),
		)
	}

	// Just validate and exit if requested
	if *validate {
		fmt.Println("Configuration is valid")
		os.Exit(0)
	}

	// Create server with config path and logger
	srv, err := server.NewServer(*configFile, logger)
	if err != nil {
		logger.Fatal("Failed to create server",
			zap.Error(err),
		)
	}

	// Setup signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		logger.Info("Received shutdown signal")
		cancel()
	}()

	// Start server
	logger.Info("Starting hapax",
		zap.String("version", Version),
		zap.Int("port", cfg.Server.Port),
	)

	if err := srv.Start(ctx); err != nil {
		logger.Fatal("Server error",
			zap.Error(err),
		)
	}
}
