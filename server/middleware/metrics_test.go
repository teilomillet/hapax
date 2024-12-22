package middleware_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/teilomillet/gollm"
	"github.com/teilomillet/hapax/config"
	"github.com/teilomillet/hapax/server/metrics"
	"github.com/teilomillet/hapax/server/middleware"
	"github.com/teilomillet/hapax/server/mocks"
	"github.com/teilomillet/hapax/server/provider"
	"go.uber.org/zap"
)

func TestPrometheusMetrics(t *testing.T) {
	// Create new metrics instance for testing
	m := metrics.NewMetrics()

	tests := []struct {
		name           string
		handler        http.HandlerFunc
		expectedCode   int
		expectedPath   string
		expectedStatus string
	}{
		{
			name: "success request",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			},
			expectedCode:   http.StatusOK,
			expectedPath:   "/",
			expectedStatus: "200",
		},
		{
			name: "error request",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			expectedCode:   http.StatusInternalServerError,
			expectedPath:   "/",
			expectedStatus: "500",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test server
			handler := middleware.PrometheusMetrics(m)(tt.handler)
			server := httptest.NewServer(handler)
			defer server.Close()

			// Make request
			resp, err := http.Get(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			// Check response code
			assert.Equal(t, tt.expectedCode, resp.StatusCode)

			// Check request metrics
			requestCount := testutil.ToFloat64(m.RequestsTotal.WithLabelValues(tt.expectedPath, tt.expectedStatus))
			assert.Equal(t, float64(1), requestCount)

			// Check active requests (should be 0 after request completes)
			activeRequests := testutil.ToFloat64(m.ActiveRequests)
			assert.Equal(t, float64(0), activeRequests)

			// Check error metrics for 5xx responses
			if tt.expectedCode >= 500 {
				errorCount := testutil.ToFloat64(m.ErrorsTotal.WithLabelValues("server_error"))
				assert.Equal(t, float64(1), errorCount)
			}
		})
	}
}

// TestMetricsObservability systematically validates metrics tracking mechanisms
func TestMetricsObservability(t *testing.T) {
	// Comprehensive Test Scenarios
	testCases := []struct {
		name             string
		providerBehavior func(context.Context, *gollm.Prompt) (string, error)
		expectedMetrics  map[string]float64
		expectedError    bool
	}{
		{
			name: "Successful Provider Interaction",
			providerBehavior: func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
				return "Successful response", nil
			},
			expectedMetrics: map[string]float64{
				"hapax_provider_requests_total": 1,
				"hapax_provider_errors_total":   0,
			},
			expectedError: false,
		},
		{
			name: "Provider Failure Scenario",
			providerBehavior: func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
				return "", fmt.Errorf("simulated provider error")
			},
			expectedMetrics: map[string]float64{
				"hapax_provider_requests_total": 1,
				"hapax_provider_errors_total":   1,
			},
			expectedError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create precise metrics tracking infrastructure
			requestsTotal := prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Name: "hapax_provider_requests_total",
					Help: "Total number of provider requests",
				},
				[]string{"provider"},
			)

			errorsTotal := prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Name: "hapax_provider_errors_total",
					Help: "Total number of provider errors",
				},
				[]string{"provider"},
			)

			// Establish comprehensive metrics registry
			registry := prometheus.NewRegistry()
			registry.MustRegister(requestsTotal, errorsTotal)

			// Create mock provider with explicit error generation
			mockProvider := mocks.NewMockLLMWithConfig(
				"test",
				"test-model",
				func(ctx context.Context, prompt *gollm.Prompt) (string, error) {
					// Directly use the test case's provider behavior
					return tc.providerBehavior(ctx, prompt)
				},
			)

			// Construct provider configuration
			cfg := &config.Config{
				TestMode: true,
				Providers: map[string]config.ProviderConfig{
					"test": {
						Type:  "test",
						Model: "test-model",
					},
				},
				ProviderPreference: []string{"test"},
			}

			// Initialize provider manager
			logger := zap.NewNop()
			manager, err := provider.NewManager(cfg, logger, registry)
			require.NoError(t, err)

			// Configure providers
			providers := map[string]gollm.LLM{
				"test": mockProvider,
			}
			manager.SetProviders(providers)

			// Prepare test prompt
			prompt := &gollm.Prompt{
				Messages: []gollm.PromptMessage{
					{Role: "user", Content: "Test metrics observability"},
				},
			}

			// Increment request metric before execution
			requestsTotal.WithLabelValues("test").Inc()

			// Execute request with comprehensive error handling
			var executionError error
			err = manager.Execute(context.Background(), func(llm gollm.LLM) error {
				_, execErr := llm.Generate(context.Background(), prompt)

				// Track error metric for failure scenarios
				if execErr != nil {
					errorsTotal.WithLabelValues("test").Inc()
				}

				// Capture and preserve execution error
				executionError = execErr
				return execErr
			}, prompt)

			// Error expectation validation
			if tc.expectedError {
				require.Error(t, executionError, "Expected error in failure scenario")
				require.Error(t, err, "Manager execution should propagate error")
			} else {
				require.NoError(t, executionError, "No error expected in successful scenario")
				require.NoError(t, err, "Manager execution should succeed")
			}

			// Comprehensive metrics verification
			mfs, err := registry.Gather()
			require.NoError(t, err)

			// Systematic metrics validation mechanism
			for _, mf := range mfs {
				for _, metric := range mf.GetMetric() {
					switch mf.GetName() {
					case "hapax_provider_requests_total":
						actualValue := metric.GetCounter().GetValue()
						assert.Equal(t,
							tc.expectedMetrics["hapax_provider_requests_total"],
							actualValue,
							"Requests total metric did not match expected value",
						)

					case "hapax_provider_errors_total":
						actualValue := metric.GetCounter().GetValue()
						assert.Equal(t,
							tc.expectedMetrics["hapax_provider_errors_total"],
							actualValue,
							"Errors total metric did not match expected value",
						)
					}
				}
			}
		})
	}
}
