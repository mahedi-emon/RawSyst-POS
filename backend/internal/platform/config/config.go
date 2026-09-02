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

	// Redis, object storage and observability are all OPTIONAL, and every
	// one of them is a deliberate decision rather than an oversight.

	Redis         Redis
	Storage       Storage
	Observability Observability

	// ZATCAEnvironment is which ZATCA stack this deployment talks to:
	// sandbox, simulation or production.
	//
	// Separate from Env, and deliberately so. A staging deployment talks to
	// ZATCA's SIMULATION stack, and a developer may point a local stack at the
	// sandbox; tying the two together would mean the only way to test against
	// simulation was to call the whole deployment "production".
	//
	// Defaults to sandbox, which is the environment where a mistake is
	// harmless. Nothing defaults to production.
	ZATCAEnvironment string
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

// Redis is the shared cache, rate-limit store and invalidation bus.
//
// # Optional, and the product is honest about what it costs to omit
//
// One API process needs none of this: an in-memory cache is faster and a
// per-process rate limit is the same limit. The moment there are TWO — a
// second replica behind the load balancer, or a rolling deploy with both
// versions up — in-memory stops being correct. A permission revoked on one
// replica stays live on the other until its own cache expires, and a rate
// limit of ten becomes a rate limit of twenty.
//
// So the fallback is real and supported for a single-process deployment,
// which is what most shops run, and Redis is what makes more than one
// process behave like one system. See internal/platform/cache.
//
// # NOT the job queue
//
// Jobs stay in Postgres (design 08): enqueuing in the same transaction as
// the thing that triggered them is worth more than a faster broker, and a
// queue a shop's accountant can query in SQL is worth more still.
type Redis struct {
	Addr     string
	Password string
	DB       int
	// TLS is for a managed Redis reached across a network somebody else runs.
	TLS bool
}

// Configured reports whether a Redis was named.
func (r Redis) Configured() bool { return strings.TrimSpace(r.Addr) != "" }

// Storage is an S3-compatible object store.
//
// # S3-compatible, not S3
//
// The endpoint is configuration, so this works against Amazon, MinIO on the
// shop's own server, Cloudflare R2, Wasabi, DigitalOcean Spaces or anything
// else that speaks the same API. That is not a nicety: a Saudi deployment
// under PDPL may be required to keep records inside the Kingdom, and tying
// the product to one vendor's regions would make that somebody else's
// decision. See internal/platform/blob.
//
// # Optional, with the database as the fallback
//
// Without it, a logo and a stored document live in Postgres as bytes, which
// is correct and does not scale — a million receipt PDFs in a table is a
// backup nobody can restore in an afternoon. With it, the database holds the
// reference and the bytes live where bytes belong.
type Storage struct {
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	// PathStyle puts the bucket in the path rather than the hostname.
	//
	// Required by MinIO and by anything reached on an IP address, because
	// `bucket.192.168.1.10` is not a name that resolves. Amazon prefers the
	// hostname form and still accepts this one.
	PathStyle bool
}

// Configured reports whether an object store was named.
func (s Storage) Configured() bool {
	return strings.TrimSpace(s.Endpoint) != "" && strings.TrimSpace(s.Bucket) != ""
}

// Observability is metrics and error reporting.
type Observability struct {
	// MetricsEnabled serves Prometheus text at /metrics.
	MetricsEnabled bool
	// MetricsToken guards it with a bearer token.
	//
	// Required outside development when metrics are on. The endpoint carries
	// tenant counts, request rates and error rates: not personal data, but a
	// precise description of how much business a shop is doing, which is not
	// something to publish on a port anybody can reach.
	MetricsToken string

	// SentryDSN turns on error reporting. Absent means off.
	SentryDSN string
	// SentrySampleRate is the fraction of TRACES sent, 0 to 1. Errors are
	// always sent; it is the performance sampling this thins out.
	SentrySampleRate float64
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
		ZATCAEnvironment: strings.ToLower(
			getString("RAWSYST_ZATCA_ENVIRONMENT", "sandbox")),
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
		Redis: Redis{
			Addr:     strings.TrimSpace(os.Getenv("RAWSYST_REDIS_ADDR")),
			Password: os.Getenv("RAWSYST_REDIS_PASSWORD"),
			DB:       getInt("RAWSYST_REDIS_DB", 0),
			TLS:      getBool("RAWSYST_REDIS_TLS", false),
		},
		Storage: Storage{
			Endpoint:        strings.TrimSpace(os.Getenv("RAWSYST_S3_ENDPOINT")),
			Region:          getString("RAWSYST_S3_REGION", "us-east-1"),
			Bucket:          strings.TrimSpace(os.Getenv("RAWSYST_S3_BUCKET")),
			AccessKeyID:     os.Getenv("RAWSYST_S3_ACCESS_KEY_ID"),
			SecretAccessKey: os.Getenv("RAWSYST_S3_SECRET_ACCESS_KEY"),
			// On by default: it works everywhere, and the deployments that
			// need it (MinIO, an IP address) are the ones where the other
			// form fails with a DNS error nobody reads as a bucket problem.
			PathStyle: getBool("RAWSYST_S3_PATH_STYLE", true),
		},
		Observability: Observability{
			MetricsEnabled:   getBool("RAWSYST_METRICS_ENABLED", true),
			MetricsToken:     strings.TrimSpace(os.Getenv("RAWSYST_METRICS_TOKEN")),
			SentryDSN:        strings.TrimSpace(os.Getenv("RAWSYST_SENTRY_DSN")),
			SentrySampleRate: getFloat("RAWSYST_SENTRY_SAMPLE_RATE", 0.1),
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

	switch cfg.ZATCAEnvironment {
	case "sandbox", "simulation", "production":
	default:
		problems = append(problems, fmt.Sprintf(
			"RAWSYST_ZATCA_ENVIRONMENT must be sandbox, simulation or production (got %q)",
			cfg.ZATCAEnvironment))
	}

	// Reporting real invoices from a deployment that does not think it is
	// production is almost always a misconfiguration, and the consequence is
	// not recoverable: the invoices are legally reported. Refused rather than
	// warned about.
	if cfg.ZATCAEnvironment == "production" && env != EnvProduction {
		problems = append(problems, fmt.Sprintf(
			"RAWSYST_ZATCA_ENVIRONMENT is production but RAWSYST_ENV is %q. "+
				"That would report real invoices to the tax authority from a "+
				"non-production deployment", env))
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

	// Half a set of object-storage credentials is a deployment that will
	// fail on the first upload rather than at start-up. Either all of it or
	// none of it.
	if cfg.Storage.Configured() {
		if strings.TrimSpace(cfg.Storage.AccessKeyID) == "" ||
			strings.TrimSpace(cfg.Storage.SecretAccessKey) == "" {
			problems = append(problems,
				"RAWSYST_S3_ENDPOINT and RAWSYST_S3_BUCKET are set, so "+
					"RAWSYST_S3_ACCESS_KEY_ID and RAWSYST_S3_SECRET_ACCESS_KEY "+
					"are required too")
		}
		if !strings.HasPrefix(cfg.Storage.Endpoint, "http://") &&
			!strings.HasPrefix(cfg.Storage.Endpoint, "https://") {
			problems = append(problems,
				"RAWSYST_S3_ENDPOINT must start with http:// or https://")
		}
		// Documents and signed invoices go over this link. Plain HTTP to an
		// object store outside the machine is a copy of a shop's records on
		// the wire, so it is refused where it would be real.
		if (env == EnvStaging || env == EnvProduction) &&
			strings.HasPrefix(cfg.Storage.Endpoint, "http://") {
			problems = append(problems,
				"RAWSYST_S3_ENDPOINT must be https outside development")
		}
	}

	// An unguarded /metrics on a public port publishes how much business
	// every shop on the stack is doing. Development is exempt because there
	// is nothing there and a token would only be pasted into a script.
	if cfg.Observability.MetricsEnabled &&
		cfg.Observability.MetricsToken == "" &&
		(env == EnvStaging || env == EnvProduction) {
		problems = append(problems,
			"RAWSYST_METRICS_TOKEN is required when metrics are served "+
				"outside development; set it, or turn metrics off with "+
				"RAWSYST_METRICS_ENABLED=false")
	}

	if r := cfg.Observability.SentrySampleRate; r < 0 || r > 1 {
		problems = append(problems, fmt.Sprintf(
			"RAWSYST_SENTRY_SAMPLE_RATE must be between 0 and 1 (got %v)", r))
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

func getBool(key string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch v {
	case "":
		return def
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

func getFloat(key string, def float64) float64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
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
