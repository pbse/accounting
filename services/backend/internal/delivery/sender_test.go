package delivery

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"strings"
	"testing"
)

func TestKrithSenderNamesTheFirmAtKrithsAddress(t *testing.T) {
	for firm, want := range map[string]string{
		"Smith & Co":                        `"Smith & Co via Krith" <requests@mail.krith.test>`,
		"  Quote \"Me\" <now>\r\nBcc: x@y ": `"Quote Me now Bcc: x@y via Krith" <requests@mail.krith.test>`,
		"Zoë Comptabilité":                  `"Zoë Comptabilité via Krith" <requests@mail.krith.test>`,
		"\"<>\\":                            `"Your accounting team via Krith" <requests@mail.krith.test>`,
	} {
		got := KrithSender(firm, "requests@mail.krith.test")
		if got != want {
			t.Fatalf("%q: %s, want %s", firm, got, want)
		}
		if parsed, err := mail.ParseAddress(got); err != nil || parsed.Address != "requests@mail.krith.test" {
			t.Fatalf("%q does not parse as one address: %v", got, err)
		}
	}
	if long := KrithSender(strings.Repeat("é", 300), "a@b.test"); len([]rune(long)) != len([]rune(`"`+strings.Repeat("é", 100)+` via Krith" <a@b.test>`)) {
		t.Fatalf("a long firm name is shortened: %s", long)
	}
	for address, want := range map[string]string{"requests@mail.krith.test": "requests@mail.krith.test", "Krith <requests@mail.krith.test>": "", "not an address": "", "": ""} {
		t.Setenv("CLIENT_EMAIL_ADDRESS", address)
		if got := KrithAddress(); got != want {
			t.Fatalf("CLIENT_EMAIL_ADDRESS %q: %q", address, got)
		}
	}
}

func TestKrithSenderSendsOnlyFromKrithsAddressWithItsOutbox(t *testing.T) {
	var sent []map[string]any
	var keys, idempotency []string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		json.Unmarshal(raw, &body)
		sent = append(sent, body)
		keys = append(keys, r.Header.Get("Authorization"))
		idempotency = append(idempotency, r.Header.Get("Idempotency-Key"))
		w.Write([]byte(`{"id":"krith-message"}`))
	}))
	defer fake.Close()
	original := resendAPI
	resendAPI = fake.URL
	t.Cleanup(func() { resendAPI = original })

	sender, err := Open("org_Any", Krith, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLIENT_EMAIL_ADDRESS", "")
	t.Setenv("PLATFORM_EMAIL_KEY", "re_platform_synthetic")
	if sender.Ready(context.Background()) == nil {
		t.Fatal("Krith's sender is ready only with its address and key")
	}
	t.Setenv("CLIENT_EMAIL_ADDRESS", "requests@mail.krith.test")
	if err = sender.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	email := Email{From: KrithSender("Smith & Co", "requests@mail.krith.test"), To: []string{"client@example.test"}, Subject: "A request", Text: "Hello"}
	if id, _, err := sender.Send(context.Background(), email, "OUTBOX1"); err != nil || id != "krith-message" {
		t.Fatalf("send: %s %v", id, err)
	}
	if len(sent) != 1 || keys[0] != "Bearer re_platform_synthetic" || idempotency[0] != "close-request/OUTBOX1" {
		t.Fatalf("Krith's key and the outbox's idempotency key: %v %v", keys, idempotency)
	}
	if tags, _ := json.Marshal(sent[0]["tags"]); string(tags) != `[{"name":"outbox","value":"OUTBOX1"}]` || sent[0]["from"] != email.From {
		t.Fatalf("the email carries its outbox's tag and the firm's name: %v", sent[0])
	}
	email.From = "Someone <someone@elsewhere.test>"
	if _, _, err := sender.Send(context.Background(), email, "OUTBOX2"); err == nil || len(sent) != 1 {
		t.Fatal("Krith's key never sends from another address")
	}
	if _, err := Open("org_Any", "postmark", nil); err == nil {
		t.Fatal("an integration this server lacks is refused")
	}
}
