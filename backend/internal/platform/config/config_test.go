package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

// A valid 32-byte key, base64, distinguishable by its fill byte.
func encodedKey(fill byte) string {
	m := make([]byte, 32)
	for i := range m {
		m[i] = fill
	}
	return base64.StdEncoding.EncodeToString(m)
}

func withEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	// A minimum viable configuration, so each test only varies what it means to.
	base := map[string]string{
		"RAWSYST_DB_DSN":     "postgres://localhost/x",
		"RAWSYST_JWT_SECRET": strings.Repeat("j", 32),
	}
	for k, v := range base {
		if _, set := kv[k]; !set {
			t.Setenv(k, v)
		}
	}
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

func TestTheKeyringIsReadNewestFirst(t *testing.T) {
	withEnv(t, map[string]string{
		"RAWSYST_DATA_ENCRYPTION_KEYS": "7:" + encodedKey(0xAA) + ",3:" + encodedKey(0xBB),
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	keys := cfg.Auth.DataEncryptionKeys
	if len(keys) != 2 {
		t.Fatalf("read %d keys, want 2", len(keys))
	}
	// Order is the contract: the first key seals new values.
	if keys[0].Version != 7 || keys[1].Version != 3 {
		t.Errorf("versions are %d then %d, want 7 then 3", keys[0].Version, keys[1].Version)
	}
	if keys[0].Material[0] != 0xAA {
		t.Error("the first key is not the one listed first")
	}
}

// The whole reason versions are explicit: a second rotation must not reuse a
// version number that is already in the database.
func TestArbitraryVersionsAreSupportedSoRotationCanContinue(t *testing.T) {
	withEnv(t, map[string]string{
		"RAWSYST_DATA_ENCRYPTION_KEYS": "255:" + encodedKey(0x11) + ",254:" + encodedKey(0x22),
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	if cfg.Auth.DataEncryptionKeys[0].Version != 255 {
		t.Errorf("got v%d, want v255", cfg.Auth.DataEncryptionKeys[0].Version)
	}
}

func TestAMalformedKeyringIsRefusedWithAUsefulMessage(t *testing.T) {
	for name, value := range map[string]string{
		"no version":      encodedKey(0xAA),
		"version zero":    "0:" + encodedKey(0xAA),
		"version too big": "256:" + encodedKey(0xAA),
		"not base64":      "1:not base64!!",
		"wrong length":    "1:" + base64.StdEncoding.EncodeToString([]byte("short")),
	} {
		t.Run(name, func(t *testing.T) {
			withEnv(t, map[string]string{"RAWSYST_DATA_ENCRYPTION_KEYS": value})
			if _, err := Load(); err == nil {
				t.Errorf("%s was accepted", name)
			}
		})
	}
}

// Development runs without one; staging and production must not.
func TestTheKeyIsRequiredOutsideDevelopment(t *testing.T) {
	withEnv(t, map[string]string{"RAWSYST_ENV": "development"})
	if _, err := Load(); err != nil {
		t.Errorf("development should not require an encryption key: %v", err)
	}

	for _, env := range []string{"staging", "production"} {
		withEnv(t, map[string]string{"RAWSYST_ENV": env})
		_, err := Load()
		if err == nil {
			t.Errorf("%s started without an encryption key", env)
			continue
		}
		if !strings.Contains(err.Error(), "RAWSYST_DATA_ENCRYPTION_KEYS") {
			t.Errorf("%s: the error does not name the missing variable: %v", env, err)
		}
	}
}

func TestAConfiguredKeyIsAcceptedInProduction(t *testing.T) {
	withEnv(t, map[string]string{
		"RAWSYST_ENV":                  "production",
		"RAWSYST_DATA_ENCRYPTION_KEYS": "1:" + encodedKey(0xAA),
	})
	if _, err := Load(); err != nil {
		t.Errorf("a correctly configured production stack was refused: %v", err)
	}
}
