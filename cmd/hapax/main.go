package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/teilomillet/gollm"
	"github.com/teilomillet/hapax/config"
	"github.com/teilomillet/hapax/server"
)

var (
	configFile = flag.String("config", "hapax.yaml", "Path to configuration file")
	validate   = flag.Bool("validate", false, "Validate configuration and exit")
	version    = flag.Bool("version", false, "Print version and exit")
)

const Version = "v0.0.2-alpha"

func main() {
	flag.Parse()

	if *version {
		fmt.Printf("hapax %s\n", Version)
		os.Exit(0)
	}

	// Load configuration
	cfg, err := config.LoadFile(*configFile)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Just validate and exit if requested
	if *validate {
		fmt.Println("Configuration is valid")
		os.Exit(0)
	}

	// Create LLM client based on config
	llm, err := createLLM(cfg.LLM)
	if err != nil {
		log.Fatalf("Failed to create LLM client: %v", err)
	}

	// Create completion handler
	handler := server.NewCompletionHandler(llm)

	// Create router with configured routes
	router := server.NewRouter(handler)

	// Create server
	srv := server.NewServer(cfg.Server, router)

	// Setup signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Received shutdown signal")
		cancel()
	}()

	// Start server
	log.Printf("Starting hapax %s on port %d", Version, cfg.Server.Port)
	if err := srv.Start(ctx); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

func createLLM(cfg config.LLMConfig) (gollm.LLM, error) {
	// Get API key from config or environment
	apiKey := cfg.APIKey
	log.Printf("API Key from config: %q", apiKey)
	
	if apiKey == "" {
		// Try environment variable as fallback
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
		log.Printf("API Key from env: %q", apiKey)
		if apiKey == "" {
			return nil, fmt.Errorf("no API key provided in config or ANTHROPIC_API_KEY environment variable")
		}
	}

	log.Printf("Creating LLM with provider=%s model=%s api_key_length=%d", cfg.Provider, cfg.Model, len(apiKey))

	// Create LLM with configuration
	llm, err := gollm.NewLLM(
		gollm.SetProvider(cfg.Provider),
		gollm.SetModel(cfg.Model),
	)
	if err != nil {
		return nil, fmt.Errorf("create LLM: %w", err)
	}

	return llm, nil
}
