package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// tests O11y's logs management and RED metrics delegation
func Test_O11y(t *testing.T) {
	// redirects JSON's structured output into a memory buffer to parse it later
	var logBuffer bytes.Buffer
	loggerHandler := slog.NewJSONHandler(&logBuffer, nil)
	slog.SetDefault(slog.New(loggerHandler))

	mockResponseWriter := httptest.NewRecorder()
	mockRequest := httptest.NewRequest(http.MethodGet, "/test-path", nil)

	// mocking the business logic to return 202 Accepted
	mockNext := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	otelHandler := O11y(mockNext)
	otelHandler.ServeHTTP(mockResponseWriter, mockRequest)

	// ensuring the proxy correctly propagated the status code to the network socket (and did not just write it in the status variable - which would cause a silent failure)
	if mockResponseWriter.Code != http.StatusAccepted {
		t.Fatalf("Expected status 202, got %d", mockResponseWriter.Code)
	}

	// ensuring the core perimeter info log is always emitted
	logOutput := logBuffer.String()
	if !strings.Contains(logOutput, "HTTP Request Processed") {
		t.Errorf("Expected log message not found in output")
	}

	// parsing the generated JSON to verify status and latency
	var logData map[string]interface{}
	err := json.Unmarshal(logBuffer.Bytes(), &logData)
	if err != nil {
		t.Fatalf("Failed to parse slog JSON output: %v", err)
	}

	if logData["status"] != float64(202) {
		t.Errorf("Expected logged status 202, got %v", logData["status"])
	}

	if _, ok := logData["latency"]; !ok {
		t.Errorf("Expected 'latency' field in log output, got nothing")
	}
}
