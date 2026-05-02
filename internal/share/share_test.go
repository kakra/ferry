package share

import "testing"

func TestEncryptDecryptToken(t *testing.T) {
	secret := "token-secret"
	token := "abc123publictoken"

	encrypted, err := EncryptToken(token, secret)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if encrypted == token || encrypted == "" {
		t.Fatalf("expected encrypted token, got %q", encrypted)
	}

	decrypted, err := DecryptToken(encrypted, secret)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if decrypted != token {
		t.Fatalf("expected %q, got %q", token, decrypted)
	}
}
