package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"
)

type Connection struct {
	Key      string `json:"key"`
	From     string `json:"from"`
	DomainID string `json:"domain_id"`
}
type Email struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Text    string   `json:"text"`
}

func request(ctx context.Context, key, method, path string, body []byte, idempotency string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, method, "https://api.resend.com"+path, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	if idempotency != "" {
		req.Header.Set("Idempotency-Key", idempotency)
	}
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("Resend response was not received; the outcome may be unknown.")
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 131073))
	if err != nil || len(raw) > 131072 {
		return nil, res.StatusCode, fmt.Errorf("Resend response was incomplete or exceeded its limit.")
	}
	return raw, res.StatusCode, nil
}
func VerifySender(ctx context.Context, c Connection) error {
	address, err := mail.ParseAddress(c.From)
	if err != nil || strings.ContainsAny(c.From, "\r\n") || len(c.From) > 320 || len(c.Key) < 10 || len(c.Key) > 500 || len(c.DomainID) > 100 || c.DomainID == "" {
		return fmt.Errorf("Provide the verified firm sender, its Resend domain ID and a valid API key.")
	}
	_, domain, ok := strings.Cut(address.Address, "@")
	if !ok {
		return fmt.Errorf("Invalid sender address.")
	}
	raw, status, err := request(ctx, c.Key, "GET", "/domains/"+url.PathEscape(c.DomainID), nil, "")
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("Resend sender verification returned HTTP %d. Check domain access and API permissions.", status)
	}
	var result struct {
		ID, Name, Status string
		ClickTracking    bool `json:"click_tracking"`
		Capabilities     struct {
			Sending string `json:"sending"`
		} `json:"capabilities"`
	}
	if json.Unmarshal(raw, &result) != nil || result.ID != c.DomainID || !strings.EqualFold(result.Name, domain) || result.Status != "verified" || result.Capabilities.Sending != "enabled" {
		return fmt.Errorf("The exact sender domain must be verified and enabled for sending in this Resend account.")
	}
	if result.ClickTracking {
		return fmt.Errorf("Disable domain click tracking before sending private request links.")
	}
	return nil
}
func Send(ctx context.Context, key string, payload []byte, id string) (string, int, error) {
	raw, status, err := request(ctx, key, "POST", "/emails", payload, "close-request/"+id)
	if err != nil {
		return "", status, err
	}
	var response struct {
		ID string `json:"id"`
	}
	if status >= 200 && status < 300 && json.Unmarshal(raw, &response) == nil && response.ID != "" && len(response.ID) <= 200 {
		return response.ID, status, nil
	}
	return "", status, fmt.Errorf("Resend did not confirm acceptance (HTTP %d).", status)
}
