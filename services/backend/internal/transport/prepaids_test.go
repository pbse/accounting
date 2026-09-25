package transport

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"close.local/backend/internal/prepaids"
)

func TestPrepareCapabilityAndPreflight(t *testing.T) {
	t.Setenv("FRONTEND_ORIGIN", "http://localhost:5173")
	handler := API()
	body := `{"entity":"Example","source_reference":"Invoice page 1","amount":"12000.00","service_start":"2026-09-01","service_end":"2027-08-31","close_period":"2026-09","currency":"USD","basis":"accrual","expense_account":"Expense","prepaid_account":"Prepaid","method":"equal_full_months_v1","method_confirmed":true,"booking_confirmed":true}`
	request := httptest.NewRequest("POST", "/v1/workpapers:prepare", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://localhost:5173")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var paper prepaids.Workpaper
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &paper) != nil || paper.PrepaidBalance != "11000.00" || response.Header().Get("Access-Control-Allow-Origin") != "http://localhost:5173" {
		t.Fatalf("invalid response: %d %s", response.Code, response.Body.String())
	}
	for _, test := range []struct {
		origin string
		status int
	}{{"http://localhost:5173", 204}, {"https://untrusted.example", 403}} {
		request = httptest.NewRequest("OPTIONS", "/v1/workpapers:prepare", nil)
		request.Header.Set("Origin", test.origin)
		request.Header.Set("Access-Control-Request-Method", "POST")
		request.Header.Set("Access-Control-Request-Headers", "content-type")
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("preflight for %s returned %d", test.origin, response.Code)
		}
	}
	request = httptest.NewRequest("OPTIONS", "/v1/client-request", nil)
	request.Header.Set("Origin", "http://localhost:5173")
	request.Header.Set("Access-Control-Request-Method", "GET")
	request.Header.Set("Access-Control-Request-Headers", "authorization, x-request-passcode")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 204 {
		t.Fatalf("a client link page may send its passcode header: %d", response.Code)
	}
	request = httptest.NewRequest("POST", "/v1/workpapers:prepare", strings.NewReader(body+` {}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 400 {
		t.Fatalf("accepted trailing JSON: %d", response.Code)
	}
}
