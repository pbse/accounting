package secrets

import (
	"strings"
	"testing"
)

func TestPreviousKeyOnlyOpensOldCiphertext(t *testing.T) {
	oldKey, newKey := strings.Repeat("11", 32), strings.Repeat("22", 32)
	t.Setenv("SECRETS_KEY", oldKey)
	old, err := Seal("firm/qbo/7", []byte("private"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SECRETS_KEY", newKey)
	t.Setenv("SECRETS_KEY_PREVIOUS", oldKey)
	value, err := OpenPrevious("firm/qbo/7", old)
	if err != nil || string(value) != "private" {
		t.Fatalf("old ciphertext lost: %q %v", value, err)
	}
	if _, err = OpenPrevious("other/qbo/7", old); err == nil {
		t.Fatal("wrong ownership opened")
	}
	newCipher, err := Seal("firm/qbo/7", value)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SECRETS_KEY_PREVIOUS", "")
	if _, err = Open("firm/qbo/7", old); err == nil {
		t.Fatal("old key still active")
	}
	value, err = Open("firm/qbo/7", newCipher)
	if err != nil || string(value) != "private" {
		t.Fatalf("new ciphertext: %q %v", value, err)
	}
}
