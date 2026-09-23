// Package secrets encrypts provider credentials and private request links at rest.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"errors"
	"os"
)

func box(name string) (cipher.AEAD, error) {
	key, err := hex.DecodeString(os.Getenv(name))
	if err != nil || len(key) != 32 {
		return nil, errors.New("Configure " + name + " as 32 random bytes encoded in hexadecimal on API and worker.")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithRandomNonce(block)
}
func Seal(scope string, plaintext []byte) ([]byte, error) {
	a, err := box("SECRETS_KEY")
	if err != nil {
		return nil, err
	}
	return a.Seal(nil, nil, plaintext, []byte(scope)), nil
}
func Open(scope string, encrypted []byte) ([]byte, error) {
	a, err := box("SECRETS_KEY")
	if err != nil {
		return nil, err
	}
	value, err := a.Open(nil, nil, encrypted, []byte(scope))
	if err != nil {
		return nil, errors.New("Encrypted configuration could not be opened; verify the encryption key and record ownership.")
	}
	return value, nil
}

func OpenPrevious(scope string, encrypted []byte) ([]byte, error) {
	a, err := box("SECRETS_KEY_PREVIOUS")
	if err != nil {
		return nil, err
	}
	value, err := a.Open(nil, nil, encrypted, []byte(scope))
	if err != nil {
		return nil, errors.New("Old ciphertext could not be opened; rotation was not applied.")
	}
	return value, nil
}
