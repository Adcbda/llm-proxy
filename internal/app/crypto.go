package app

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Vault struct {
	aead cipher.AEAD
}

func LoadOrCreateVault(dataDir, databasePath string) (*Vault, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	keyPath := filepath.Join(dataDir, "master.key")
	key, err := os.ReadFile(keyPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read master key: %w", err)
		}
		if _, statErr := os.Stat(databasePath); statErr == nil {
			return nil, fmt.Errorf("database exists but %s is missing; restore the matching master key", keyPath)
		}
		key = make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, key); err != nil {
			return nil, fmt.Errorf("generate master key: %w", err)
		}
		file, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, fmt.Errorf("create master key: %w", err)
		}
		if _, err = file.Write(key); err != nil {
			file.Close()
			return nil, fmt.Errorf("write master key: %w", err)
		}
		if err = file.Close(); err != nil {
			return nil, fmt.Errorf("close master key: %w", err)
		}
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("master key must contain exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("initialize vault: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize AES-GCM: %w", err)
	}
	return &Vault{aead: aead}, nil
}

func (v *Vault) Encrypt(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate encryption nonce: %w", err)
	}
	sealed := v.aead.Seal(nil, nonce, []byte(value), []byte("llm-proxy:v1"))
	payload := append(nonce, sealed...)
	return "v1:" + base64.RawURLEncoding.EncodeToString(payload), nil
}

func (v *Vault) Decrypt(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if !strings.HasPrefix(value, "v1:") {
		return "", fmt.Errorf("unsupported encrypted value version")
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, "v1:"))
	if err != nil {
		return "", fmt.Errorf("decode encrypted value: %w", err)
	}
	if len(payload) < v.aead.NonceSize() {
		return "", fmt.Errorf("encrypted value is too short")
	}
	nonce, ciphertext := payload[:v.aead.NonceSize()], payload[v.aead.NonceSize():]
	plaintext, err := v.aead.Open(nil, nonce, ciphertext, []byte("llm-proxy:v1"))
	if err != nil {
		return "", fmt.Errorf("decrypt value: %w", err)
	}
	return string(plaintext), nil
}

func GenerateProjectKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", fmt.Errorf("generate project key: %w", err)
	}
	return "llmp_" + base64.RawURLEncoding.EncodeToString(buf), nil
}

func HashProjectKey(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func NewID(prefix string) (string, error) {
	buf := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(buf), nil
}

func MaskSecret(value string) string {
	if value == "" {
		return ""
	}
	if len(value) <= 8 {
		return "••••••••"
	}
	return value[:4] + "••••" + value[len(value)-4:]
}
