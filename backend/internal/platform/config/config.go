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

	"github.com/mahedi-emon/Biz1core/backend/internal/platform/secrets"
)

// Config is the fully resolved runtime configuration.
type Config struct {
	Env         Environment
	HTTP        HTTP
	DB          DB
	Auth        Auth
	DataRegion  string // sa | eu | asia | other — the region THIS stack serves
	ServiceName string

	// AppURL is where a business signs in, as an outsider would type it.
	//
	// The product never needs this to serve a request -- every route is
	// same-origin and reached by relative path. It is needed the moment
	// something has to TELL somebody where the product is: the welcome message
	// a new owner gets names an address, and an address the server has to guess
	// from a request header is one an attacker can choose.
	//
	// Empty is honest rather than broken. The address is then simply left out
	// of the message instead of a wrong one being asserted, and the operator
	// handing the account over says it themselves.
	//
	// This is the BUSINESS origin, never the control plane's. See ConsoleURL
	// for the other one.
	AppURL string

	// ConsoleHost is the hostname the platform control plane answers on.
	//
	// Empty means the two halves share one origin, which is how every
	// deployment worked before the split and how a developer's machine works
	// now. Setting it turns the separation on: platform routes answer only on
	// this host, and business routes answer only on every other host.
	//
	// A hostname, not a URL: this is compared against the `Host` header, so it
	// carries no scheme and no path. The port is ignored when comparing,
	// because a browser sends one and a configuration file usually does not.
	//
	// # Read here as well as by the web tier
	//
	// Both tiers need it and they enforce different halves. The web tier
	// decides which PAGES a hostname serves; this decides which API ROUTES it
	// serves. A split enforced only at the edge is one that a direct call to
	// the API walks straight past.
	ConsoleHost string

	// ConsoleURL is the console's full address, for the rare case where
	// something has to NAME it rather than compare against it.
	//
	// Deliberately separate from ConsoleHost rather than derived: the scheme
	// may be http behind a proxy that terminates TLS, and a port may be in
	// play in development. Guessing "https://" + host would be wrong in both.
	//
	// It is never sent to an unauthenticated caller. The console's address is
	// not a secret in any strong sense — anybody who can resolve DNS can find
	// it — but publishing it to every shop in a JavaScript bundle would undo
	// the modest benefit of not linking to it anywhere.
	ConsoleURL string

	// Mail is how this deployment sends a message to a person.
	//
	// Optional, and empty is a supported state rather than a broken one: the
	// worker then logs in development and refuses anywhere else, which is what
	// it has always done. What it never does is pretend.
	Mail Mail

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
		ServiceName: getString("RAWSYST_SERVICE_NAME", "biz1core-api"),
		DataRegion:  strings.ToLower(getString("RAWSYST_DATA_REGION", "sa")),
		AppURL:      strings.TrimRight(strings.TrimSpace(getString("RAWSYST_APP_URL", "")), "/"),
		// Lower-cased and stripped of any scheme somebody pasted in, because a
		// hostname compared against a `Host` header has neither. Getting a URL
		// here instead of a hostname is the most likely way to configure this
		// wrongly, and silently never matching would look like the split
		// simply not working.
		ConsoleHost: hostOnly(getString("RAWSYST_CONSOLE_HOST", "")),
		ConsoleURL:  strings.TrimRight(strings.TrimSpace(getString("RAWSYST_CONSOLE_URL", "")), "/"),
		Mail: Mail{
			ResendAPIKey: strings.TrimSpace(getString("RAWSYST_RESEND_API_KEY", "")),
			From:         strings.TrimSpace(getString("RAWSYST_MAIL_FROM", "")),
		},
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
			Issuer:          getString("RAWSYST_JWT_ISSUER", "biz1core-pos"),
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

// hostOnly reduces whatever was configured to a bare, comparable hostname.
//
// A `Host` header carries no scheme and no path, so a value that has either
// would never match anything and the split would look like it was simply not
// working. Accepting the URL somebody pasted, and reducing it, is better than
// refusing to start over a form of the right answer.
//
// The port is kept if it was given: a development console on :3001 is a real
// case, and the comparison strips ports on both sides rather than here.
// Mail names the provider and the sender.
//
// # Why the sender is configuration and not a constant
//
// Resend, like every provider worth using, will only send from a domain
// somebody has verified with SPF and DKIM. That domain belongs to whoever runs
// the deployment, so the address cannot be written into the product.
type Mail struct {
	// ResendAPIKey is the credential. NEVER logged, never put in an error, and
	// never sent anywhere but Resend. `Configured` is the only thing anything
	// else is told about it.
	ResendAPIKey string

	// From is the sender, and it must be on a domain verified in Resend.
	// Resend refuses anything else, permanently, which is correct: an
	// unverified domain does not become verified by retrying.
	From string
}

// Configured reports whether mail can actually be sent.
//
// Both halves, because either one missing means every message is refused —
// and a deployment that set the key and forgot the sender would otherwise
// believe it had mail working right up until the first password reset.
func (m Mail) Configured() bool {
	return m.ResendAPIKey != "" && m.From != ""
}

func hostOnly(raw string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	if i := strings.Index(v, "://"); i >= 0 {
		v = v[i+3:]
	}
	if i := strings.IndexAny(v, "/?#"); i >= 0 {
		v = v[:i]
	}
	return v
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
