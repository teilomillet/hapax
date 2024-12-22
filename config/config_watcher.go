// config_watcher.go
package config

import (
	"fmt"
	"sync/atomic"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"
)

// Verify at compile time that ConfigWatcher implements Watcher
var _ Watcher = (*ConfigWatcher)(nil)

// ConfigWatcher manages configuration hot reloading
type ConfigWatcher struct {
	// Using atomic.Value for thread-safe config access
	currentConfig atomic.Value
	configPath    string
	watcher       *fsnotify.Watcher
	logger        *zap.Logger
	// Channel to notify subscribers of config changes
	subscribers []chan<- *Config
}

// NewConfigWatcher creates a new configuration watcher
func NewConfigWatcher(configPath string, logger *zap.Logger) (*ConfigWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create watcher: %w", err)
	}

	cw := &ConfigWatcher{
		configPath: configPath,
		watcher:    watcher,
		logger:     logger,
	}

	// Load initial configuration
	initialConfig, err := LoadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load initial config: %w", err)
	}
	cw.currentConfig.Store(initialConfig)

	// Start watching the config file
	if err := watcher.Add(configPath); err != nil {
		return nil, fmt.Errorf("failed to watch config file: %w", err)
	}

	go cw.watchConfig()
	return cw, nil
}

// Subscribe allows components to receive config updates
func (cw *ConfigWatcher) Subscribe() <-chan *Config {
	ch := make(chan *Config, 1)
	cw.subscribers = append(cw.subscribers, ch)
	return ch
}

// GetCurrentConfig returns the current configuration thread-safely
func (cw *ConfigWatcher) GetCurrentConfig() *Config {
	return cw.currentConfig.Load().(*Config)
}

func (cw *ConfigWatcher) watchConfig() {
	for {
		select {
		case event, ok := <-cw.watcher.Events:
			if !ok {
				return
			}
			if event.Op&fsnotify.Write == fsnotify.Write {
				cw.handleConfigChange()
			}
		case err, ok := <-cw.watcher.Errors:
			if !ok {
				return
			}
			cw.logger.Error("Config watcher error", zap.Error(err))
		}
	}
}

func (cw *ConfigWatcher) handleConfigChange() {
	cw.logger.Info("Detected config file change, reloading...")

	newConfig, err := LoadFile(cw.configPath)
	if err != nil {
		cw.logger.Error("Failed to load new config", zap.Error(err))
		return
	}

	// Validate the new configuration
	if err := newConfig.Validate(); err != nil {
		cw.logger.Error("Invalid new configuration", zap.Error(err))
		return
	}

	// Store the new configuration
	cw.currentConfig.Store(newConfig)

	// Notify all subscribers
	for _, sub := range cw.subscribers {
		select {
		case sub <- newConfig:
		default:
			// Skip if subscriber is not ready
		}
	}

	cw.logger.Info("Configuration reloaded successfully")
}

func (cw *ConfigWatcher) Close() error {
	return cw.watcher.Close()
}
