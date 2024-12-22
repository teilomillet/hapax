package middleware

type contextKey string

const (
	RequestIDKey contextKey = "request_id"
	XTestTimeoutKey contextKey = "X-Test-Timeout"
)
