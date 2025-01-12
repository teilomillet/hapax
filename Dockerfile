# Build stage
FROM golang:1.22-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git gcc musl-dev

# Set working directory
WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o hapax ./cmd/hapax

# Final stage
FROM alpine:3.19

# Add non-root user
RUN adduser -D -g '' hapax

# Install runtime dependencies
RUN apk add --no-cache ca-certificates tzdata curl

# Set working directory
WORKDIR /app

# Copy binary and configuration files
COPY --from=builder /app/hapax .

COPY config.example.yaml ./config.yaml
COPY docker-compose.yml ./docker-compose.yml
COPY prometheus.yml ./prometheus.yml

# Use non-root user
USER hapax

# Expose ports
EXPOSE 8081

# Set healthcheck
HEALTHCHECK --interval=10s --timeout=5s --start-period=10s --retries=3 \
  CMD curl -f http://localhost:8081/health || exit 1

# Run the application
ENTRYPOINT ["./hapax"]

CMD ["--config", "config.yaml"]