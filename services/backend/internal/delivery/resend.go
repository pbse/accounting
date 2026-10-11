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
	"os"
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
	ReplyTo string   `json:"reply_to,omitempty"`
	Subject string   `json:"subject"`
	Text    string   `json:"text"`
	HTML    string   `json:"html,omitempty"`
	// Tags are added as the email goes out (OutboxTag), never stored with the approved email.
	Tags []Tag `json:"tags,omitempty"`
}
type Tag struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// resendProvider is a firm's own Resend account: a sender on a domain verified there, the domain's ID, and a key that
// can read that domain and send from it.
var resendProvider = Provider{
	Name:  "resend",
	Label: "Resend",
	Connect: func(ctx context.Context, fields map[string]string) (string, string, []byte, error) {
		c := Connection{Key: strings.TrimSpace(fields["key"]), From: strings.TrimSpace(fields["from"]), DomainID: strings.TrimSpace(fields["domain_id"])}
		if err := VerifySender(ctx, c); err != nil {
			return "", "", nil, err
		}
		settings, err := json.Marshal(c)
		return c.From, c.DomainID, settings, err
	},
	Open: func(settings []byte) (Sender, error) {
		var c Connection
		if json.Unmarshal(settings, &c) != nil {
			return nil, fmt.Errorf("Invalid encrypted sender configuration")
		}
		return resendSender(c), nil
	},
}

// resendSender sends through the firm's own Resend account, checking its domain before each email.
type resendSender Connection

func (s resendSender) Ready(ctx context.Context) error {
	return VerifySender(ctx, Connection(s))
}

func (s resendSender) Send(ctx context.Context, message Email, outbox string) (string, int, error) {
	payload, err := json.Marshal(message)
	if err != nil {
		return "", 0, err
	}
	return send(ctx, s.Key, payload, "close-request/"+outbox)
}

// resendAPI is Resend's API; tests point it at a fake.
var resendAPI = "https://api.resend.com"

func request(ctx context.Context, key, method, path string, body []byte, idempotency string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, method, resendAPI+path, bytes.NewReader(body))
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

// VerifyKrithSender checks Krith's own sender (PLATFORM_EMAIL_KEY, CLIENT_EMAIL_ADDRESS) as VerifySender checks a firm's,
// finding its domain in the account by name; it reads only and sends nothing.
func VerifyKrithSender(ctx context.Context) error {
	from, key := KrithAddress(), os.Getenv("PLATFORM_EMAIL_KEY")
	address, err := mail.ParseAddress(from)
	if err != nil {
		return fmt.Errorf("CLIENT_EMAIL_ADDRESS is not an email address.")
	}
	_, domain, _ := strings.Cut(address.Address, "@")
	raw, status, err := request(ctx, key, "GET", "/domains", nil, "")
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("Resend listing the domains returned HTTP %d. Check the key's domain access.", status)
	}
	var list struct {
		Data []struct{ ID, Name string }
	}
	if json.Unmarshal(raw, &list) != nil {
		return fmt.Errorf("Resend's domain list could not be read.")
	}
	for _, d := range list.Data {
		if strings.EqualFold(d.Name, domain) {
			return VerifySender(ctx, Connection{Key: key, From: from, DomainID: d.ID})
		}
	}
	return fmt.Errorf("The sender's domain %s is not in this Resend account.", domain)
}

// send posts one email under an idempotency key, which Resend honours for 24 hours.
func send(ctx context.Context, key string, payload []byte, idempotency string) (string, int, error) {
	raw, status, err := request(ctx, key, "POST", "/emails", payload, idempotency)
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
