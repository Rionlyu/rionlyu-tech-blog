package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOverloadRejectsImmediatelyAndKeepsHealthAvailable(t *testing.T) {
	t.Parallel()
	server := &Server{inFlight: make(chan struct{}, 1)}
	server.inFlight <- struct{}{}

	called := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusNoContent)
	})
	handler := server.security(server.overload(next))

	request := httptest.NewRequest(http.MethodGet, "/projects", nil)
	request.Header.Set("Accept", "text/html,application/xhtml+xml")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") != "5" {
		t.Fatalf("busy response = %d, Retry-After = %q", response.Code, response.Header().Get("Retry-After"))
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("busy response headers = %#v", response.Header())
	}
	if !strings.Contains(response.Body.String(), "Just a moment") || called != 0 {
		t.Fatalf("busy response body = %q, next calls = %d", response.Body.String(), called)
	}

	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusNoContent || called != 1 {
		t.Fatalf("health response = %d, next calls = %d", health.Code, called)
	}

	<-server.inFlight
	available := httptest.NewRecorder()
	handler.ServeHTTP(available, httptest.NewRequest(http.MethodGet, "/projects", nil))
	if available.Code != http.StatusNoContent || called != 2 {
		t.Fatalf("available response = %d, next calls = %d", available.Code, called)
	}
}
