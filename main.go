package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	// Application version from environment variable with default
	version string
	// Backend URL from environment variable with default
	backendURL string
	// Track application start time for uptime calculation
	startTime = time.Now()
	// Logger for access logs
	accessLogger = log.New(os.Stdout, "ACCESS: ", log.LstdFlags)
	// Metrics
	metrics = NewMetrics()
)

// Initialize environment variables with defaults
func init() {
	// Set VERSION with default "1.0.0"
	version = os.Getenv("VERSION")
	if version == "" {
		version = "1.0.0"
	}

	// Set BACKEND with default "http://localhost:8080/version"
	backendURL = os.Getenv("BACKEND")
	if backendURL == "" {
		backendURL = "http://localhost:8080/version"
	}
}

// Metrics tracks request statistics
type Metrics struct {
	mutex             sync.RWMutex
	totalRequests     map[string]int64         // Counter for total requests by path
	statusCodes       map[string]map[int]int64 // Counter for status codes by path
	requestDurations  map[string][]float64     // Histogram data for request durations
	appStartTimestamp int64                    // Timestamp when the application started
}

// NewMetrics creates a new Metrics instance
func NewMetrics() *Metrics {
	return &Metrics{
		totalRequests:     make(map[string]int64),
		statusCodes:       make(map[string]map[int]int64),
		requestDurations:  make(map[string][]float64),
		appStartTimestamp: time.Now().Unix(),
	}
}

// RecordRequest records metrics for a request
func (m *Metrics) RecordRequest(path string, statusCode int, duration time.Duration) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	// Clean path for metric name (replace non-alphanumeric chars with underscore)

	cleanPath := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9' || r == '/') {
			return r
		}
		return '_'
	}, path)
	fmt.Printf("Path: %s : %s\n", path, cleanPath)
	if cleanPath == "" || cleanPath[0] == '_' {
		cleanPath = "root" + cleanPath
	}

	// Increment total requests counter
	m.totalRequests[cleanPath]++

	// Increment status code counter
	if _, exists := m.statusCodes[cleanPath]; !exists {
		m.statusCodes[cleanPath] = make(map[int]int64)
	}
	m.statusCodes[cleanPath][statusCode]++

	// Record request duration
	m.requestDurations[cleanPath] = append(m.requestDurations[cleanPath], duration.Seconds())
}

// GetPrometheusMetrics returns metrics in Prometheus format
func (m *Metrics) GetPrometheusMetrics() string {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	var sb strings.Builder

	// Application info metric
	sb.WriteString("# HELP app_info Information about the application\n")
	sb.WriteString("# TYPE app_info gauge\n")
	sb.WriteString(fmt.Sprintf("app_info{version=\"%s\"} 1\n\n", version))

	// Application uptime metric
	sb.WriteString("# HELP app_uptime_seconds How long the application has been running\n")
	sb.WriteString("# TYPE app_uptime_seconds counter\n")
	sb.WriteString(fmt.Sprintf("app_uptime_seconds %d\n\n", time.Now().Unix()-m.appStartTimestamp))

	// Request counter metric
	sb.WriteString("# HELP http_requests_total Total number of HTTP requests\n")
	sb.WriteString("# TYPE http_requests_total counter\n")
	for path, count := range m.totalRequests {
		sb.WriteString(fmt.Sprintf("http_requests_total{path=\"%s\"} %d\n", path, count))
	}
	sb.WriteString("\n")

	// Status code counter metric
	sb.WriteString("# HELP http_response_status_total HTTP response status codes\n")
	sb.WriteString("# TYPE http_response_status_total counter\n")
	for path, codes := range m.statusCodes {
		for code, count := range codes {
			sb.WriteString(fmt.Sprintf("http_response_status_total{path=\"%s\",code=\"%d\"} %d\n", path, code, count))
		}
	}
	sb.WriteString("\n")

	// Request duration histogram
	sb.WriteString("# HELP http_request_duration_seconds HTTP request duration in seconds\n")
	sb.WriteString("# TYPE http_request_duration_seconds histogram\n")
	// Define buckets for the histogram
	buckets := []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

	for path, durations := range m.requestDurations {
		// Calculate counts for each bucket
		bucketCounts := make([]int, len(buckets)+1)
		var sum float64

		for _, d := range durations {
			sum += d
			// Count in which bucket this duration falls
			for i, b := range buckets {
				if d <= b {
					bucketCounts[i]++
				}
			}
			bucketCounts[len(buckets)]++ // Count in the +Inf bucket
		}

		// Write the bucket observations
		for i, b := range buckets {
			sb.WriteString(fmt.Sprintf("http_request_duration_seconds_bucket{path=\"%s\",le=\"%g\"} %d\n",
				path, b, bucketCounts[i]))
		}
		sb.WriteString(fmt.Sprintf("http_request_duration_seconds_bucket{path=\"%s\",le=\"+Inf\"} %d\n",
			path, bucketCounts[len(buckets)]))

		// Write sum and count
		sb.WriteString(fmt.Sprintf("http_request_duration_seconds_sum{path=\"%s\"} %g\n", path, sum))
		sb.WriteString(fmt.Sprintf("http_request_duration_seconds_count{path=\"%s\"} %d\n", path, len(durations)))
	}

	return sb.String()
}

// AccessLogMiddleware logs details about incoming requests
func AccessLogMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		requestStart := time.Now()

		// Create a responseWriter that captures the status code
		rw := &responseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK, // Default status code
		}

		// Call the next handler
		next(rw, r)

		// Calculate request duration
		duration := time.Since(requestStart)

		// Log the request details
		accessLogger.Printf("%s - \"%s %s %s\" %d %s - %s",
			r.RemoteAddr,
			r.Method,
			r.URL.Path,
			r.Proto,
			rw.statusCode,
			r.Header.Get("User-Agent"),
			duration,
		)

		// Record metrics
		metrics.RecordRequest(r.URL.Path, rw.statusCode, duration)
	}
}

// responseWriter is a wrapper around http.ResponseWriter that captures the status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

// WriteHeader captures the status code before writing it
func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// ForwardToBackend forwards the request to the backend URL
func ForwardToBackend(w http.ResponseWriter, r *http.Request) {
	// Only process requests for root path "/"
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	// Create a new request to the backend
	req, err := http.NewRequest(r.Method, backendURL, r.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error creating request: %v", err), http.StatusInternalServerError)
		return
	}

	// Copy headers from original request
	for name, values := range r.Header {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}

	// Send the request to the backend
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error forwarding to backend: %v", err), http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for name, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}

	// Set response status code
	w.WriteHeader(resp.StatusCode)

	// Copy response body
	_, err = io.Copy(w, resp.Body)
	if err != nil {
		log.Printf("Error copying response body: %v", err)
	}
}

// VersionHandler returns the application version
func VersionHandler(w http.ResponseWriter, r *http.Request) {
	// Only process requests for exact "/version" path
	if r.URL.Path != "/version" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprintf(w, "Version: %s\n", version)
}

// LivenessHandler checks if the application is live
func LivenessHandler(w http.ResponseWriter, r *http.Request) {
	// Only process requests for exact "/health/live" path
	if r.URL.Path != "/health/live" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"status":"UP","uptime":"%s"}`, time.Since(startTime).String())
}

// ReadinessHandler checks if the application is ready to serve requests
func ReadinessHandler(w http.ResponseWriter, r *http.Request) {
	// Only process requests for exact "/health/ready" path
	if r.URL.Path != "/health/ready" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status":"UP","backend":"%s"}`, backendURL)
}

// MetricsHandler exposes application metrics in Prometheus format
func MetricsHandler(w http.ResponseWriter, r *http.Request) {
	// Only process requests for exact "/metrics" path
	if r.URL.Path != "/metrics" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprint(w, metrics.GetPrometheusMetrics())
}

// NotFoundHandler handles requests to undefined paths
func NotFoundHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	fmt.Fprintf(w, `{"status":"Not Found","message":"The requested URI does not exist","path":"%s"}`, r.URL.Path)
}

func main() {
	// Log configuration on startup
	log.Printf("Starting server with VERSION=%s and BACKEND=%s", version, backendURL)

	// Create a custom ServeMux to handle routes
	mux := http.NewServeMux()

	// Register routes
	mux.HandleFunc("/", AccessLogMiddleware(ForwardToBackend))
	mux.HandleFunc("/version", AccessLogMiddleware(VersionHandler))
	mux.HandleFunc("/health/live", AccessLogMiddleware(LivenessHandler))
	mux.HandleFunc("/health/ready", AccessLogMiddleware(ReadinessHandler))
	mux.HandleFunc("/metrics", AccessLogMiddleware(MetricsHandler))

	// Start the server with the custom handler
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Server starting on port %s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}
