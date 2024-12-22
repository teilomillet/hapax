package config

// Watcher defines the behavior we expect from any configuration watcher
type Watcher interface {
	GetCurrentConfig() *Config
	Subscribe() <-chan *Config
	Close() error
}
