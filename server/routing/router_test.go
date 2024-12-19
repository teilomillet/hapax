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
	"go.uber.org/zap"
)

// TestRouter_NewRouter tests the creation of a new router
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

	// Test
	router := NewRouter(cfg, handlers, logger)

	// Assert
	assert.NotNil(t, router)
	assert.NotNil(t, router.router)
	assert.Equal(t, handlers, router.handlers)
}

// TestRouter_VersionedRouting tests that versioned routes are properly handled
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
	router := NewRouter(cfg, handlers, zap.NewNop())

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

// TestRouter_HeaderValidation tests that header validation works correctly
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
	router := NewRouter(cfg, handlers, zap.NewNop())

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

// TestRouter_MethodRestriction tests that method restrictions are enforced
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
	router := NewRouter(cfg, handlers, zap.NewNop())

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

// TestRouter_HealthCheck tests the health check functionality
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
	router := NewRouter(cfg, handlers, zap.NewNop())

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

// TestRouter_Middleware tests that middleware is properly applied
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

	router := &Router{
		router:   r,
		handlers: handlers,
		logger:   zap.NewNop(),
		cfg:      cfg,
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
