package main

import "testing"

func TestOnlyALocalDatabaseIsUsed(t *testing.T) {
	for url, ok := range map[string]bool{
		"postgres://close:x@127.0.0.1:5432/db": true, "postgres://close:x@localhost/db": true, "postgresql://close:x@[::1]:5432/db": true,
		"postgres:///db?host=/tmp": true, "postgres://close:x@db.example.com/db": false, "postgres:///db?host=db.example.com": false,
		"host=db.example.com dbname=db": false, "postgres://close:x@10.0.0.5/db": false,
	} {
		if err := local(url); (err == nil) != ok {
			t.Errorf("%s: %v", url, err)
		}
	}
}
