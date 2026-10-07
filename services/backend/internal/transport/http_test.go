package transport

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The worked example is no longer public (it is Krith's own; see examples.go), and there is no generic action route.
func TestNoPublicExampleOrActionRoute(t *testing.T) {
	response := httptest.NewRecorder()
	API().ServeHTTP(response, httptest.NewRequest("GET", "/v1/reference/prepaid", nil))
	if response.Code != 404 {
		t.Fatalf("the example is not public: %d", response.Code)
	}
	for _, test := range []struct {
		handler http.Handler
		path    string
		status  int
	}{
		{API(), "/v1/actions", 404},
		{Worker(), "/internal/jobs", 503},
	} {
		response = httptest.NewRecorder()
		test.handler.ServeHTTP(response, httptest.NewRequest("POST", test.path, nil))
		if response.Code != test.status {
			t.Fatalf("%s returned %d", test.path, response.Code)
		}
	}
}

func TestCrossOriginAccess(t *testing.T) {
	t.Setenv("FRONTEND_ORIGIN", "https://close.example")
	handler := API()
	for _, origin := range []string{"https://close.example", "https://untrusted.example", ""} {
		request := httptest.NewRequest("GET", "/healthz", nil)
		request.Header.Set("Origin", origin)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		expected := ""
		if origin == "https://close.example" {
			expected = origin
		}
		if response.Code != 200 || response.Header().Get("Access-Control-Allow-Origin") != expected || response.Header().Get("Vary") != "Origin" {
			t.Fatalf("unexpected cross-origin response for %q: %d %v", origin, response.Code, response.Header())
		}
	}
}
