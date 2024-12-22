package mocks

import (
	"sync/atomic"

	"github.com/teilomillet/hapax/config"
)

// MockConfigWatcher provides a testable implementation of config.Watcher
type MockConfigWatcher struct {
	currentConfig atomic.Value
	subscribers   []chan *config.Config
}

// Verify at compile time that MockConfigWatcher implements config.Watcher
var _ config.Watcher = (*MockConfigWatcher)(nil)

// NewMockConfigWatcher creates a new MockConfigWatcher initialized with the provided config
func NewMockConfigWatcher(cfg *config.Config) *MockConfigWatcher {
	mcw := &MockConfigWatcher{
		subscribers: make([]chan *config.Config, 0),
	}
	mcw.currentConfig.Store(cfg)
	return mcw
}

// GetCurrentConfig implements config.Watcher
func (m *MockConfigWatcher) GetCurrentConfig() *config.Config {
	return m.currentConfig.Load().(*config.Config)
}

// Subscribe implements config.Watcher
func (m *MockConfigWatcher) Subscribe() <-chan *config.Config {
	ch := make(chan *config.Config, 1)
	m.subscribers = append(m.subscribers, ch)

	// Send current config immediately
	cfg := m.GetCurrentConfig()
	select {
	case ch <- cfg:
	default:
	}

	return ch
}

// Close implements config.Watcher
func (m *MockConfigWatcher) Close() error {
	for _, ch := range m.subscribers {
		close(ch)
	}
	m.subscribers = nil
	return nil
}

// UpdateConfig is a test helper that simulates configuration changes
func (m *MockConfigWatcher) UpdateConfig(cfg *config.Config) {
	m.currentConfig.Store(cfg)

	for _, ch := range m.subscribers {
		select {
		case ch <- cfg:
		default:
			// Skip if channel is blocked
		}
	}
}
