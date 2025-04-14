# Build stage
FROM golang:1.24-alpine AS builder

# Set working directory
WORKDIR /app

# Copy go.mod and go.sum files
COPY go.mod go.sum* ./

# Download dependencies (if go.sum exists)
RUN if [ -f go.sum ]; then go mod download; else go mod tidy; fi

# Copy the source code
COPY *.go ./

# Build the application for linux/amd64 platform
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -installsuffix cgo -o api .

# Final stage with RHEL 9 UBI minimal
FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

# Set working directory
WORKDIR /app

# Copy the binary from builder stage
COPY --from=builder /app/api .

# Create a non-root user for running the application
RUN microdnf install -y shadow-utils && \
    groupadd -r appuser && \
    useradd -r -g appuser -s /sbin/nologin appuser && \
    chown -R appuser:appuser /app && \
    microdnf clean all

# Switch to non-root user
USER appuser

# Expose the port the app runs on
EXPOSE 8080

# Define environment variables with defaults
ENV VERSION="1.0.0"
ENV BACKEND="http://localhost:8080/version"
ENV PORT="8080"

# Run the API
CMD ["./api"]