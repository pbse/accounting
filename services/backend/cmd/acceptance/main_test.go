package main

import "testing"

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
