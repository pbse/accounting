package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestServiceFailurePreservesStatusWithoutProviderContent(t *testing.T) {
	for _, status := range []int{400, 401, 403, 429, 502, 503, 504} {
		calls := 0
		c := &Client{http: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("SECRET_PROVIDER_BODY"))}, nil
		})}}
		_, err := c.post(context.Background(), "https://provider.test/v1", "SECRET_TOKEN", map[string]string{"prompt": "SECRET_PROMPT"})
		var service *ServiceError
		if !errors.As(err, &service) || service.Status != status || calls != 1 || strings.Contains(err.Error(), "SECRET") {
			t.Fatalf("%d %v %d", status, err, calls)
		}
		if errors.Is(err, ErrBusy) != (status == 429) {
			t.Fatal("busy classification lost")
		}
		code, got := FailureCode(fmt.Errorf("SECRET_WRAPPER: %w", err))
		if got != status || strings.Contains(code, "SECRET") || status == 504 && code != "timeout" {
			t.Fatalf("%s %d", code, got)
		}
	}
	for _, tc := range []struct {
		err  error
		code string
	}{{context.DeadlineExceeded, "timeout"}, {context.Canceled, "canceled"}, {ErrIncomplete, "incomplete"}, {ErrLimit, "limit"}, {ErrUnreachable, "unavailable"}, {ErrUnavailable, "unavailable"}, {errors.New("SECRET"), "error"}} {
		if code, status := FailureCode(fmt.Errorf("wrapped: %w", tc.err)); code != tc.code || status != 0 {
			t.Fatalf("%s %d", code, status)
		}
	}
}

func TestOnlyPrivateCompletionAcceptsKnownFailureCodes(t *testing.T) {
	for _, tc := range []struct {
		body   any
		header string
		want   bool
	}{
		{map[string]string{"prompt": "safe"}, "incomplete", false},
		{CompletionRequest{}, "incomplete", true},
		{CompletionRequest{}, "SECRET_HEADER", false},
	} {
		c := &Client{http: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 502, Header: http.Header{"X-Context-Machine-Failure": []string{tc.header}}, Body: io.NopCloser(strings.NewReader("SECRET_BODY"))}, nil
		})}}
		_, err := c.post(context.Background(), "https://provider.test/v1", "fake", tc.body)
		if errors.Is(err, ErrIncomplete) != tc.want {
			t.Fatalf("header %q classification %v", tc.header, err)
		}
		code, _ := FailureCode(err)
		if strings.Contains(code, "SECRET") {
			t.Fatal("untrusted header reached diagnostics")
		}
	}
}
