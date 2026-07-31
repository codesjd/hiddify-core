package hutils

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
)

// GenerateAndPersistServiceToken creates a new random token and writes it to
// a file named "<name>.token" next to the given directory, with permissions
// restricting it to the current user (0600). Call this once, from the
// elevated service process itself, when it starts.
func GenerateAndPersistServiceToken(dir, name string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b[:])
	path := filepath.Join(dir, name+".token")
	if err := os.WriteFile(path, []byte(token), 0o600); err != nil {
		return "", err
	}
	return token, nil
}

// ReadServiceToken reads back a token written by GenerateAndPersistServiceToken.
func ReadServiceToken(dir, name string) (string, error) {
	path := filepath.Join(dir, name+".token")
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
