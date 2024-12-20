// Package processing provides request processing and response formatting for LLM interactions.
package processing

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"text/template"

	"github.com/teilomillet/gollm"
	"github.com/teilomillet/hapax/config"
)

// Processor handles request processing and response formatting for LLM interactions.
// It uses Go templates to transform incoming requests into LLM-compatible formats,
// communicates with the LLM, and formats the responses according to configuration.
//
// Key features:
// - Template-based request transformation
// - Configurable response formatting
// - Support for both simple and chat completions
// - System prompt management
//
// The Processor is designed to be reusable across different request types
// while maintaining consistent formatting and error handling.
type Processor struct {
	llm           gollm.LLM                    // The LLM instance to use for generation
	templates     map[string]*template.Template // Compiled templates for request formatting
	config        *config.ProcessingConfig      // Configuration for processing behavior
	defaultPrompt string                        // Default system prompt for all requests
}

// NewProcessor creates a new processor instance with the given configuration and LLM.
// It validates the configuration and pre-compiles all templates for efficiency.
//
// Parameters:
// - cfg: Processing configuration including templates and formatting options
// - llm: LLM instance to use for text generation
//
// Returns:
// - A new Processor instance and nil error if successful
// - nil and error if configuration is invalid or template compilation fails
//
// The processor will fail fast if any templates are invalid, preventing runtime errors.
func NewProcessor(cfg *config.ProcessingConfig, llm gollm.LLM) (*Processor, error) {
	if cfg == nil {
		return nil, fmt.Errorf("processing config is required")
	}
	if llm == nil {
		return nil, fmt.Errorf("LLM instance is required")
	}

	// Parse all templates at initialization to fail fast on invalid templates
	templates := make(map[string]*template.Template)
	for name, tmpl := range cfg.RequestTemplates {
		t, err := template.New(name).Parse(tmpl)
		if err != nil {
			return nil, fmt.Errorf("failed to parse template %s: %w", name, err)
		}
		templates[name] = t
	}

	return &Processor{
		llm:       llm,
		templates: templates,
		config:    cfg,
	}, nil
}

// ProcessRequest handles the end-to-end processing of a request:
// 1. Validates the request
// 2. Selects and executes the appropriate template
// 3. Creates an LLM prompt with system context
// 4. Sends the request to the LLM
// 5. Formats the response according to configuration
//
// Parameters:
// - ctx: Context for the request, used for cancellation and timeouts
// - req: The request to process, containing type and input data
//
// Returns:
// - Formatted response and nil error if successful
// - nil and error if any step fails
//
// The processor will use the "default" template if no matching template
// is found for the request type.
func (p *Processor) ProcessRequest(ctx context.Context, req *Request) (*Response, error) {
	if req == nil {
		return nil, fmt.Errorf("request cannot be nil")
	}

	// Select the appropriate template, falling back to default
	tmpl := p.templates["default"]
	if t, ok := p.templates[req.Type]; ok {
		tmpl = t
	}
	if tmpl == nil {
		return nil, fmt.Errorf("no template found for type: %s", req.Type)
	}

	// Execute the template with the request data
	var buf bytes.Buffer
	err := tmpl.Execute(&buf, req)
	if err != nil {
		return nil, fmt.Errorf("template execution failed: %w", err)
	}

	// Create an LLM prompt with system context
	prompt := &gollm.Prompt{
		Messages: []gollm.PromptMessage{
			{
				Role:    "system",
				Content: p.defaultPrompt,
			},
			{
				Role:    "user",
				Content: buf.String(),
			},
		},
	}

	// Pass timeout header to LLM context if present
	if timeoutHeader := ctx.Value("X-Test-Timeout"); timeoutHeader != nil {
		ctx = context.WithValue(ctx, "X-Test-Timeout", timeoutHeader)
	}

	// Send request to LLM
	response, err := p.llm.Generate(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM processing failed: %w", err)
	}

	// Apply response formatting
	return p.formatResponse(response), nil
}

// formatResponse applies configured formatting options to the LLM response:
// 1. Cleans JSON if enabled (removes markdown blocks, formats JSON)
// 2. Trims whitespace if enabled
// 3. Truncates to max length if configured
//
// This ensures consistent response format and size across different
// LLM outputs and request types.
func (p *Processor) formatResponse(content string) *Response {
	if p.config.ResponseFormatting.CleanJSON {
		content = gollm.CleanResponse(content)
	}
	if p.config.ResponseFormatting.TrimWhitespace {
		content = strings.TrimSpace(content)
	}
	if p.config.ResponseFormatting.MaxLength > 0 && len(content) > p.config.ResponseFormatting.MaxLength {
		content = content[:p.config.ResponseFormatting.MaxLength]
	}
	return &Response{Content: content}
}

// SetDefaultPrompt sets the system prompt to be used for all requests.
// This prompt provides context and instructions to the LLM.
func (p *Processor) SetDefaultPrompt(prompt string) {
	p.defaultPrompt = prompt
}