package delivery

import (
	"context"
	"encoding/json"
	"fmt"
	"net/mail"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"close.local/backend/internal/secrets"
)

// Who sends a firm's client email (docs/email-delivery.md#senders). Every firm starts with Krith's own sender, which
// sends from Krith's verified domain with Krith's key; a firm may connect an integration of its own instead. Each such
// integration is a Provider: what the firm enters to connect it, how that is checked and stored, and how an approved
// email goes out through it. A new integration adds a Provider to Providers, its name to email_connections' provider
// check (migration 142) and its form to Settings; approval, the outbox, reminders and delivery tracking stay as they are.

// Krith is the provider name of Krith's own sender.
const Krith = "krith"

// A Sender sends one firm's approved emails through one integration.
type Sender interface {
	// Ready confirms, before each send, that the sender may carry private links now.
	Ready(ctx context.Context) error
	// Send posts the outbox's approved email under the outbox's idempotency key, and returns the provider's message ID
	// and the HTTP status (0 when no answer arrived).
	Send(ctx context.Context, message Email, outbox string) (string, int, error)
}

// A Provider is an integration a firm can connect to send its own client email.
type Provider struct {
	// Name is stored in email_connections.provider and scopes the encrypted settings.
	Name string `json:"name"`
	// Label is how Settings names it.
	Label string `json:"label"`
	// Connect checks what the firm entered and returns the sender its email will come from, a public reference kept
	// with it (Resend's domain ID) and the settings to encrypt.
	Connect func(ctx context.Context, fields map[string]string) (sender, reference string, settings []byte, err error) `json:"-"`
	// Open makes the sender from the decrypted settings.
	Open func(settings []byte) (Sender, error) `json:"-"`
}

// Providers are the integrations a firm can connect, in the order Settings offers them.
var Providers = []Provider{resendProvider}

// Lookup is the provider a firm can connect by that name.
func Lookup(name string) (Provider, bool) {
	for _, p := range Providers {
		if p.Name == name {
			return p, true
		}
	}
	return Provider{}, false
}

// Open is the sender of one of a firm's connections: Krith's own, or the integration the firm connected, from its
// encrypted settings.
func Open(org, provider string, settings []byte) (Sender, error) {
	if provider == Krith {
		return krithSender{}, nil
	}
	p, ok := Lookup(provider)
	if !ok {
		return nil, fmt.Errorf("This server cannot send through %q.", provider)
	}
	plain, err := secrets.Open(SettingsScope(org, provider), settings)
	if err != nil {
		return nil, err
	}
	return p.Open(plain)
}

// SettingsScope binds a connection's encrypted settings to its firm and provider.
func SettingsScope(org, provider string) string {
	return org + "/" + provider
}

// KrithAddress is the address Krith sends firms' client email from (CLIENT_EMAIL_ADDRESS, on Krith's verified sending
// domain), or "" when this server has none. Krith's sender needs it and Krith's own Resend key (PLATFORM_EMAIL_KEY) on
// the worker, and its signing secret (CLIENT_EMAIL_WEBHOOK_SECRET) on the API to track delivery.
func KrithAddress() string {
	address := strings.TrimSpace(os.Getenv("CLIENT_EMAIL_ADDRESS"))
	parsed, err := mail.ParseAddress(address)
	if err != nil || parsed.Name != "" || parsed.Address != address || len(address) > 200 || !strings.Contains(address, "@") {
		return ""
	}
	return address
}

// KrithSender is the From of a firm's email through Krith's sender: the firm's name, "via Krith", at Krith's address.
// Characters that would break the header are dropped, and a long name is shortened.
func KrithSender(firm, address string) string {
	name := strings.Join(strings.FieldsFunc(firm, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }), " ")
	name = strings.Map(func(r rune) rune {
		if r == '"' || r == '\\' || r == '<' || r == '>' || r == utf8.RuneError {
			return -1
		}
		return r
	}, name)
	if runes := []rune(strings.TrimSpace(name)); len(runes) > 100 {
		name = string(runes[:100])
	}
	if name = strings.TrimSpace(name); name == "" {
		name = "Your accounting team"
	}
	return `"` + name + ` via Krith" <` + address + `>`
}

// KrithWebhookSecret is the signing secret of Krith's own Resend webhook, or "" when this server has none.
func KrithWebhookSecret() string {
	if secret := os.Getenv("CLIENT_EMAIL_WEBHOOK_SECRET"); ValidWebhookSecret(secret) {
		return secret
	}
	return ""
}

// OutboxTag names the outbox an email of Krith's sender came from. Every firm's email through Krith shares Krith's
// Resend account and webhook, and Resend returns an email's tags with each of its events, so this tag is how an event
// finds its firm, even one that arrives before the send's answer.
const OutboxTag = "outbox"

// krithSender sends with Krith's key, and only from Krith's address.
type krithSender struct{}

func (krithSender) Ready(context.Context) error {
	if os.Getenv("PLATFORM_EMAIL_KEY") == "" || KrithAddress() == "" {
		return fmt.Errorf("Krith's sender is not set up on this server.")
	}
	return nil
}

func (krithSender) Send(ctx context.Context, message Email, outbox string) (string, int, error) {
	from, err := mail.ParseAddress(message.From)
	if err != nil || !strings.EqualFold(from.Address, KrithAddress()) {
		return "", 0, fmt.Errorf("This email is not from Krith's sending address.")
	}
	message.Tags = []Tag{{Name: OutboxTag, Value: outbox}}
	payload, err := json.Marshal(message)
	if err != nil {
		return "", 0, err
	}
	return send(ctx, os.Getenv("PLATFORM_EMAIL_KEY"), payload, "close-request/"+outbox)
}
