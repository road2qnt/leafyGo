package secret

import (
	"testing"
)

func TestBoxRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	box, err := New(key)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ct, err := box.Encrypt("s3cr3t-password")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if len(ct) == 0 {
		t.Fatal("empty ciphertext")
	}
	pt, err := box.Decrypt(ct)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if pt != "s3cr3t-password" {
		t.Fatalf("Decrypt = %q", pt)
	}
}

func TestBoxRejectsBadKey(t *testing.T) {
	if _, err := New([]byte("short")); err == nil {
		t.Fatal("expected error for short key")
	}
}

func TestBoxRejectsTamperedCiphertext(t *testing.T) {
	key := make([]byte, 32)
	box, err := New(key)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ct, err := box.Encrypt("hello")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	ct[len(ct)-1] ^= 0xff
	if _, err := box.Decrypt(ct); err == nil {
		t.Fatal("expected error for tampered ciphertext")
	}
}
