package routing

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/teilomillet/hapax/config"
	"github.com/teilomillet/hapax/server/metrics"
	"go.uber.org/zap"
)

func setupTestRouter(t *testing.T) (*Router, *httptest.Server) {
	// Create logger
	logger, _ := zap.NewDevelopment()

	// Create metrics
	m := metrics.NewMetrics()

	// Create test handler
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Create handlers map
	handlers := map[string]http.Handler{
		"test": handler,
	}

	// Create config
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Path:    "/test",
				Handler: "test",
				Version: "v1",
				Methods: []string{"GET"},
			},
		},
	}

	// Create router
	router := NewRouter(cfg, handlers, logger, m)

	// Create test server
	server := httptest.NewServer(router)

	return router, server
}

func TestRouter_NewRouter(t *testing.T) {
	// Setup
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Path:    "/test",
				Handler: "test",
				Version: "v1",
			},
		},
	}
	handlers := map[string]http.Handler{
		"test": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	}
	logger := zap.NewNop()
	m := metrics.NewMetrics()

	// Test
	router := NewRouter(cfg, handlers, logger, m)

	// Assert
	assert.NotNil(t, router)
	assert.NotNil(t, router.router)
	assert.Equal(t, handlers, router.handlers)
}

func TestRouter_VersionedRouting(t *testing.T) {
	// Setup
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Path:    "/test",
				Handler: "test",
				Version: "v1",
			},
			{
				Path:    "/test",
				Handler: "test2",
				Version: "v2",
			},
		},
	}
	handlers := map[string]http.Handler{
		"test": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("v1"))
		}),
		"test2": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("v2"))
		}),
	}
	logger := zap.NewNop()
	m := metrics.NewMetrics()
	router := NewRouter(cfg, handlers, logger, m)

	// Test V1
	req := httptest.NewRequest("GET", "/v1/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, "v1", w.Body.String())

	// Test V2
	req = httptest.NewRequest("GET", "/v2/test", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, "v2", w.Body.String())
}

func TestRouter_HeaderValidation(t *testing.T) {
	// Setup
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Path:    "/test",
				Handler: "test",
				Version: "v1",
				Headers: map[string]string{
					"X-Required": "test-value",
				},
			},
		},
	}
	handlers := map[string]http.Handler{
		"test": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	}
	logger := zap.NewNop()
	m := metrics.NewMetrics()
	router := NewRouter(cfg, handlers, logger, m)

	// Test without required header
	req := httptest.NewRequest("GET", "/v1/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// Test with required header
	req = httptest.NewRequest("GET", "/v1/test", nil)
	req.Header.Set("X-Required", "test-value")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRouter_MethodRestriction(t *testing.T) {
	// Setup
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Path:    "/test",
				Handler: "test",
				Version: "v1",
				Methods: []string{"POST"},
			},
		},
	}
	handlers := map[string]http.Handler{
		"test": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	}
	logger := zap.NewNop()
	m := metrics.NewMetrics()
	router := NewRouter(cfg, handlers, logger, m)

	// Test with wrong method
	req := httptest.NewRequest("GET", "/v1/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)

	// Test with correct method
	req = httptest.NewRequest("POST", "/v1/test", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRouter_HealthCheck(t *testing.T) {
	// Setup
	cfg := &config.Config{
		Server: config.ServerConfig{
			Port: 8081,
		},
		Routes: []config.RouteConfig{
			{
				Path:    "/test",
				Handler: "test",
				Version: "v1",
				HealthCheck: &config.HealthCheck{
					Enabled:   true,
					Interval:  time.Second,
					Timeout:   time.Second,
					Threshold: 1,
					Checks: map[string]string{
						"http": "http",
					},
				},
			},
		},
	}
	handlers := map[string]http.Handler{
		"test": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	}
	logger := zap.NewNop()
	m := metrics.NewMetrics()
	router := NewRouter(cfg, handlers, logger, m)

	// Test route health check
	req := httptest.NewRequest("GET", "/v1/test/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]string
	err := json.NewDecoder(w.Body).Decode(&resp)
	assert.NoError(t, err)
	assert.Equal(t, "healthy", resp["status"])

	// Test global health check
	req = httptest.NewRequest("GET", "/health", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var globalResp struct {
		Status struct {
			Global bool `json:"global"`
		} `json:"status"`
		Services map[string]string `json:"services"`
	}
	err = json.NewDecoder(w.Body).Decode(&globalResp)
	assert.NoError(t, err)
	assert.True(t, globalResp.Status.Global)
	assert.NotEmpty(t, globalResp.Services)
}

func TestRouter_Middleware(t *testing.T) {
	// Setup
	middlewareCalled := false
	testMiddleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			middlewareCalled = true
			next.ServeHTTP(w, r)
		})
	}

	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Path:       "/test",
				Handler:    "test",
				Version:    "v1",
				Middleware: []string{"custom"},
			},
		},
	}

	// Create router with custom middleware
	r := chi.NewRouter()
	r.Use(testMiddleware)

	handlers := map[string]http.Handler{
		"test": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	}

	logger := zap.NewNop()
	m := metrics.NewMetrics()
	router := &Router{
		router:   r,
		handlers: handlers,
		logger:   logger,
		cfg:      cfg,
		metrics:  m,
	}
	router.setupRoutes()

	// Test
	req := httptest.NewRequest("GET", "/v1/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Assert
	assert.True(t, middlewareCalled)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRouterBasic(t *testing.T) {
	_, server := setupTestRouter(t)
	defer server.Close()

	// Test valid endpoint
	resp, err := http.Get(server.URL + "/v1/test")
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Test invalid endpoint
	resp, err = http.Get(server.URL + "/invalid")
	assert.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()
}

func TestRouterMetrics(t *testing.T) {
	_, server := setupTestRouter(t)
	defer server.Close()

	// Test metrics endpoint
	resp, err := http.Get(server.URL + "/metrics")
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}
