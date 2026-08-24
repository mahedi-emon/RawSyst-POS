// Package config loads runtime configuration from the environment.
//
// Configuration is read once at startup and treated as immutable. Anything
// that can legitimately change while the process runs — legal rates, tenant
// limits, feature availability — belongs in the database, not here. In
// particular, no tax rate, threshold, deadline or file format may ever appear
// in this package: see docs/system-design/05-regulatory-rule-registry.md.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/secrets"
)

// Config is the fully resolved runtime configuration.
type Config struct {
	Env         Environment
	HTTP        HTTP
	DB          DB
	Auth        Auth
	DataRegion  string // sa | eu | asia | other — the region THIS stack serves
	ServiceName string
}

// Environment distinguishes deployments. It gates behaviour that must never
// be active in production, such as permitting unverified regulatory rules.
type Environment string

const (
	EnvDevelopment Environment = "development"
	EnvStaging     Environment = "staging"
	EnvProduction  Environment = "production"
)

func (e Environment) IsProduction() bool { return e == EnvProduction }

// HTTP holds server transport settings.
type HTTP struct {
	Addr            string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
}

// DB holds PostgreSQL connection settings.
type DB struct {
	DSN             string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
}

// Auth holds token and password settings.
//
// Secrets are read from the environment here, but in production they must be
// injected from a secret manager rather than a file on disk. The ZATCA device
// STAMPING key is deliberately absent: it never reaches the server at all, and
// lives only in the POS terminal's OS keystore, which
// docs/system-design/01-invoice-zatca-engine.md §7 records as a locked rule.
//
// DataEncryptionKey is a different thing and belongs here: it protects the
// credentials the server legitimately holds -- the CSID ZATCA issues and the
// secret that authenticates the reporting and clearance calls. §7 assigns the
// cloud exactly that role, "onboarding credentials and the compliance-CSID
// request flow only".
type Auth struct {
	JWTSecret       []byte
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	Issuer          string

	// DataEncryptionKeys are the keyring for secrets stored in the database,
	// newest first. More than one only during a rotation: the first seals new
	// values, the rest still open old ones.
	DataEncryptionKeys []secrets.Key
}

// Load reads configuration from the environment, applies defaults, and
// validates the result. It returns every problem it finds at once rather than
// failing on the first, so a misconfigured deployment can be fixed in one pass.
func Load() (Config, error) {
	var problems []string

	env := Environment(getString("RAWSYST_ENV", string(EnvDevelopment)))
	switch env {
	case EnvDevelopment, EnvStaging, EnvProduction:
	default:
		problems = append(problems, fmt.Sprintf(
			"RAWSYST_ENV must be development, staging or production (got %q)", env))
	}

	cfg := Config{
		Env:         env,
		ServiceName: getString("RAWSYST_SERVICE_NAME", "rawsyst-api"),
		DataRegion:  strings.ToLower(getString("RAWSYST_DATA_REGION", "sa")),
		HTTP: HTTP{
			Addr:            getString("RAWSYST_HTTP_ADDR", ":8080"),
			ReadTimeout:     getDuration("RAWSYST_HTTP_READ_TIMEOUT", 15*time.Second),
			WriteTimeout:    getDuration("RAWSYST_HTTP_WRITE_TIMEOUT", 30*time.Second),
			IdleTimeout:     getDuration("RAWSYST_HTTP_IDLE_TIMEOUT", 120*time.Second),
			ShutdownTimeout: getDuration("RAWSYST_HTTP_SHUTDOWN_TIMEOUT", 20*time.Second),
		},
		DB: DB{
			DSN:             os.Getenv("RAWSYST_DB_DSN"),
			MaxConns:        int32(getInt("RAWSYST_DB_MAX_CONNS", 20)),
			MinConns:        int32(getInt("RAWSYST_DB_MIN_CONNS", 2)),
			MaxConnLifetime: getDuration("RAWSYST_DB_MAX_CONN_LIFETIME", time.Hour),
			MaxConnIdleTime: getDuration("RAWSYST_DB_MAX_CONN_IDLE_TIME", 30*time.Minute),
		},
		Auth: Auth{
			JWTSecret:       []byte(os.Getenv("RAWSYST_JWT_SECRET")),
			AccessTokenTTL:  getDuration("RAWSYST_ACCESS_TOKEN_TTL", 15*time.Minute),
			RefreshTokenTTL: getDuration("RAWSYST_REFRESH_TOKEN_TTL", 720*time.Hour),
			Issuer:          getString("RAWSYST_JWT_ISSUER", "rawsyst-pos"),
		},
	}

	switch cfg.DataRegion {
	case "sa", "eu", "asia", "other":
	default:
		problems = append(problems, fmt.Sprintf(
			"RAWSYST_DATA_REGION must be sa, eu, asia or other (got %q)", cfg.DataRegion))
	}

	if cfg.DB.DSN == "" {
		problems = append(problems, "RAWSYST_DB_DSN is required")
	}

	// A short or absent signing secret is a silent authentication bypass, so it
	// is a hard failure everywhere rather than a production-only check.
	const minSecretLen = 32
	if len(cfg.Auth.JWTSecret) < minSecretLen {
		problems = append(problems, fmt.Sprintf(
			"RAWSYST_JWT_SECRET must be at least %d bytes (got %d)",
			minSecretLen, len(cfg.Auth.JWTSecret)))
	}

	// The keyring for credentials at rest, newest key first.
	//
	// The version is written explicitly as "<version>:<base64>" rather than
	// implied by position, and that matters: an implicit scheme where the
	// current key is always v1 and the previous always v2 survives exactly one
	// rotation. On the second, a fresh "v1" would collide with the original v1
	// and every value sealed under it would fail its tag check -- unreadable
	// credentials with no way back, which is the worst failure this package can
	// have.
	//
	// Required in staging and production and OPTIONAL in development: a
	// developer running the stack locally has no ZATCA credentials to protect,
	// and demanding a key to run the test suite would get a throwaway one
	// committed within a week. Where it is absent, storing a credential fails
	// loudly at the point of storing rather than silently writing plaintext.
	if raw := strings.TrimSpace(os.Getenv("RAWSYST_DATA_ENCRYPTION_KEYS")); raw != "" {
		for _, entry := range strings.Split(raw, ",") {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			version, encoded, ok := strings.Cut(entry, ":")
			if !ok {
				problems = append(problems,
					"RAWSYST_DATA_ENCRYPTION_KEYS entries look like \"1:<base64>\", "+
						"newest first")
				continue
			}
			v, err := strconv.Atoi(strings.TrimSpace(version))
			if err != nil || v < 1 || v > 255 {
				problems = append(problems, fmt.Sprintf(
					"RAWSYST_DATA_ENCRYPTION_KEYS: %q is not a version between 1 and 255",
					version))
				continue
			}
			key, err := secrets.ParseKey(byte(v), strings.TrimSpace(encoded))
			if err != nil {
				problems = append(problems, "RAWSYST_DATA_ENCRYPTION_KEYS: "+err.Error())
				continue
			}
			if len(key.Material) != secrets.KeyLength {
				problems = append(problems, fmt.Sprintf(
					"RAWSYST_DATA_ENCRYPTION_KEYS: key v%d must decode to %d bytes (got %d)",
					v, secrets.KeyLength, len(key.Material)))
				continue
			}
			cfg.Auth.DataEncryptionKeys = append(cfg.Auth.DataEncryptionKeys, key)
		}
	} else if env == EnvStaging || env == EnvProduction {
		problems = append(problems,
			"RAWSYST_DATA_ENCRYPTION_KEYS is required outside development: it "+
				"protects the ZATCA CSID secret at rest, and without it a "+
				"deployment cannot store the credentials it needs to report invoices")
	}

	if cfg.DB.MinConns > cfg.DB.MaxConns {
		problems = append(problems, "RAWSYST_DB_MIN_CONNS cannot exceed RAWSYST_DB_MAX_CONNS")
	}

	if len(problems) > 0 {
		return Config{}, fmt.Errorf("invalid configuration:\n  - %s",
			strings.Join(problems, "\n  - "))
	}
	return cfg, nil
}

func getString(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func getDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}
