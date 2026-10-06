package main

import (
	"strings"
	"testing"

	"close.local/backend/internal/acceptance"
	"close.local/backend/internal/transport"
)

// The check follows the host the driver would use: from the setting, its fallbacks, or PGHOST when the setting names none.
func TestOnlyALocalDatabaseIsUsed(t *testing.T) {
	t.Setenv("PGHOST", "")
	for setting, ok := range map[string]bool{
		"postgres://close:x@127.0.0.1:5432/db": true, "postgres://close:x@localhost/db": true, "postgresql://close:x@[::1]:5432/db": true,
		"postgres:///db?host=/tmp": true, "host=127.0.0.1 dbname=db": true, "postgres://close:x@db.example.com/db": false,
		"postgres:///db?host=db.example.com": false, "host=db.example.com dbname=db": false, "postgres://close:x@10.0.0.5/db": false,
		"postgres://close:x@127.0.0.1,db.example.com/db": false,
	} {
		if err := local(setting); (err == nil) != ok {
			t.Errorf("%s: %v", setting, err)
		}
	}
	t.Setenv("PGHOST", "db.example.com")
	if err := local("postgres:///acceptance"); err == nil {
		t.Error("a setting without a host takes PGHOST, which here is remote")
	}
}

// In one named firm each case is a close of that firm, so two cases may not share a client.
func TestOneFirmNeedsADistinctClientPerCase(t *testing.T) {
	cases := []acceptance.Case{{ID: "a", Client: "Harbor LLC"}, {ID: "b", Client: "Pier LLC"}}
	if err := oneFirm(transport.AcceptanceFirm{}, append(cases, acceptance.Case{ID: "c", Client: "harbor llc"})); err != nil {
		t.Fatalf("a firm per case allows any clients: %v", err)
	}
	firm := transport.AcceptanceFirm{Organization: "org_Owner", Subject: "user_1"}
	if err := oneFirm(firm, cases); err != nil {
		t.Fatal(err)
	}
	if err := oneFirm(firm, append(cases, acceptance.Case{ID: "c", Client: " harbor llc"})); err == nil || !strings.Contains(err.Error(), "cases a and c share the client") {
		t.Fatalf("a shared client in one firm: %v", err)
	}
	if err := oneFirm(transport.AcceptanceFirm{Organization: "org_with-dash", Subject: "user_1"}, cases); err == nil {
		t.Fatal("a firm ID the database refuses")
	}
}
