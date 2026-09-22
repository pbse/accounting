package transport

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReferenceAndActionBoundary(t *testing.T) {
	response := httptest.NewRecorder()
	API().ServeHTTP(response, httptest.NewRequest("GET", "/v1/reference/prepaid", nil))
	var paper struct {
		Mode     string
		Schedule []struct {
			Expense   string
			Remaining string
		}
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &paper) != nil || paper.Mode != "synthetic" || len(paper.Schedule) != 12 || paper.Schedule[11].Remaining != "0.00" {
		t.Fatalf("reference contract invalid: %s", response.Body.String())
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

func TestReferenceCrossOriginAccess(t *testing.T) {
	t.Setenv("FRONTEND_ORIGIN", "https://close.example")
	handler := API()
	for _, origin := range []string{"https://close.example", "https://untrusted.example", ""} {
		request := httptest.NewRequest("GET", "/v1/reference/prepaid", nil)
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
