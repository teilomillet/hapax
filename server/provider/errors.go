package provider

import "errors"

var (
	// ErrNoHealthyProvider indicates that no healthy provider is available
	ErrNoHealthyProvider = errors.New("no healthy provider available")
)
