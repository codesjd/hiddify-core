package hutils

import "testing"

func TestGenerateAndPersistServiceToken_RoundTrips(t *testing.T) {
	dir := t.TempDir()
	token, err := GenerateAndPersistServiceToken(dir, "test")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(token) != 32 { // 16 bytes hex-encoded
		t.Fatalf("expected 32-char hex token, got %d chars", len(token))
	}
	read, err := ReadServiceToken(dir, "test")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if read != token {
		t.Fatalf("expected %q, got %q", token, read)
	}
}
