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
		"RAWSYST_METRICS_TOKEN":        "a-scrape-token",
	})
	if _, err := Load(); err != nil {
		t.Errorf("a correctly configured production stack was refused: %v", err)
	}
}

// The scrape endpoint carries a description of how much business every shop on
// a stack is doing. It is not personal data and it is not something to publish
// on a port anybody can reach, so serving it unguarded outside development is
// refused rather than warned about.
func TestTheScrapeTokenIsRequiredOutsideDevelopment(t *testing.T) {
	base := map[string]string{
		"RAWSYST_DATA_ENCRYPTION_KEYS": "1:" + encodedKey(0xAA),
	}

	// Development is exempt: there is nothing there, and demanding a token to
	// run the stack locally gets a throwaway one committed within a week.
	withEnv(t, merged(base, map[string]string{"RAWSYST_ENV": "development"}))
	if _, err := Load(); err != nil {
		t.Errorf("development should not require a scrape token: %v", err)
	}

	for _, env := range []string{"staging", "production"} {
		// Set explicitly rather than left to the default: `withEnv` adds to
		// the environment and does not clear it, so the previous iteration's
		// "false" would still be set and this case would pass for the wrong
		// reason.
		withEnv(t, merged(base, map[string]string{
			"RAWSYST_ENV":             env,
			"RAWSYST_METRICS_ENABLED": "true",
		}))
		_, err := Load()
		if err == nil {
			t.Errorf("%s served metrics with no token", env)
			continue
		}
		if !strings.Contains(err.Error(), "RAWSYST_METRICS_TOKEN") {
			t.Errorf("%s: the error does not name the variable: %v", env, err)
		}

		// And turning metrics off is the other way out, which the message
		// says. A deployment that does not want a token must not be stuck.
		withEnv(t, merged(base, map[string]string{
			"RAWSYST_ENV":             env,
			"RAWSYST_METRICS_ENABLED": "false",
		}))
		if _, err := Load(); err != nil {
			t.Errorf("%s refused to start with metrics switched off: %v", env, err)
		}
	}
}

// TestTheSampleRateHasToBeAFraction: a rate above one is somebody meaning
// "100%" and getting a client that behaves unpredictably instead.
func TestTheSampleRateHasToBeAFraction(t *testing.T) {
	withEnv(t, map[string]string{
		"RAWSYST_ENV":                  "production",
		"RAWSYST_DATA_ENCRYPTION_KEYS": "1:" + encodedKey(0xAA),
		"RAWSYST_METRICS_TOKEN":        "a-scrape-token",
		"RAWSYST_SENTRY_SAMPLE_RATE":   "100",
	})
	if _, err := Load(); err == nil {
		t.Error("a sample rate of 100 was accepted")
	}
}

// TestHalfAnObjectStoreIsRefused: a deployment with an endpoint and no key
// fails on the first upload rather than at start-up, which is the wrong place
// to find out.
func TestHalfAnObjectStoreIsRefused(t *testing.T) {
	withEnv(t, map[string]string{
		"RAWSYST_ENV":         "development",
		"RAWSYST_S3_ENDPOINT": "https://s3.example.com",
		"RAWSYST_S3_BUCKET":   "rawsyst",
	})
	_, err := Load()
	if err == nil {
		t.Fatal("an object store with no credentials was accepted")
	}
	if !strings.Contains(err.Error(), "RAWSYST_S3_ACCESS_KEY_ID") {
		t.Errorf("the error does not name what is missing: %v", err)
	}
}

// TestObjectStorageMustBeEncryptedOutsideDevelopment: documents and signed
// invoices go over that link, and plain HTTP to a store outside the machine is
// a copy of a shop's records on the wire.
func TestObjectStorageMustBeEncryptedOutsideDevelopment(t *testing.T) {
	env := map[string]string{
		"RAWSYST_ENV":                  "production",
		"RAWSYST_DATA_ENCRYPTION_KEYS": "1:" + encodedKey(0xAA),
		"RAWSYST_METRICS_TOKEN":        "a-scrape-token",
		"RAWSYST_S3_ENDPOINT":          "http://minio:9000",
		"RAWSYST_S3_BUCKET":            "rawsyst",
		"RAWSYST_S3_ACCESS_KEY_ID":     "key",
		"RAWSYST_S3_SECRET_ACCESS_KEY": "secret",
	}
	withEnv(t, env)
	if _, err := Load(); err == nil {
		t.Error("plain HTTP object storage was accepted in production")
	}

	env["RAWSYST_S3_ENDPOINT"] = "https://minio:9000"
	withEnv(t, env)
	if _, err := Load(); err != nil {
		t.Errorf("https object storage was refused: %v", err)
	}
}

// merged is one map over another, so a table of cases can share a base without
// mutating it.
func merged(base, over map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		out[k] = v
	}
	return out
}
