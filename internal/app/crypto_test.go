package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVaultRoundTripAndMissingKeyProtection(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	databasePath := filepath.Join(dir, "llm-proxy.db")
	vault, err := LoadOrCreateVault(dir, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	secret := "sk-upstream-super-secret"
	encrypted, err := vault.Encrypt(secret)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encrypted, secret) {
		t.Fatal("encrypted value contains plaintext")
	}
	decrypted, err := vault.Decrypt(encrypted)
	if err != nil || decrypted != secret {
		t.Fatalf("round trip = %q, %v", decrypted, err)
	}
	if err := os.WriteFile(databasePath, []byte("database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "master.key")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateVault(dir, databasePath); err == nil || !strings.Contains(err.Error(), "restore") {
		t.Fatalf("expected missing-key protection, got %v", err)
	}
}

func TestProjectKeyFormatAndHash(t *testing.T) {
	t.Parallel()
	first, err := GenerateProjectKey()
	if err != nil {
		t.Fatal(err)
	}
	second, _ := GenerateProjectKey()
	if !strings.HasPrefix(first, "llmp_") || first == second {
		t.Fatalf("unexpected generated keys %q %q", first, second)
	}
	if HashProjectKey(first) == HashProjectKey(second) {
		t.Fatal("different keys must have different hashes")
	}
}
