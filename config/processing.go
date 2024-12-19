package config

// ProcessingConfig defines the configuration for request/response processing
type ProcessingConfig struct {
	// RequestTemplates maps template names to their content
	RequestTemplates map[string]string `yaml:"request_templates"`

	// ResponseFormatting configures how responses should be formatted
	ResponseFormatting ResponseFormattingConfig `yaml:"response_formatting"`
}

// ResponseFormattingConfig defines response formatting options
type ResponseFormattingConfig struct {
	// CleanJSON enables JSON response cleaning using gollm
	CleanJSON bool `yaml:"clean_json"`

	// TrimWhitespace removes extra whitespace from responses
	TrimWhitespace bool `yaml:"trim_whitespace"`

	// MaxLength limits the response length
	MaxLength int `yaml:"max_length"`
}
