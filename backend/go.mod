module github.com/mahedi-emon/rawsyst-pos/backend

go 1.26

// The four stdlib advisories govulncheck reports (crypto/tls, net/http,
// encoding/xml, encoding/asn1) are fixed in 1.26.6. Pinning the toolchain here
// keeps a local build and CI on the same patched compiler rather than relying
// on whatever each machine happens to have.
toolchain go1.26.6

require (
	github.com/btcsuite/btcd/btcec/v2 v2.5.0
	github.com/coder/websocket v1.8.15
	github.com/getsentry/sentry-go v0.49.0
	github.com/go-chi/chi/v5 v5.3.0
	github.com/golang-jwt/jwt/v5 v5.2.2
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.9.2
	github.com/redis/go-redis/v9 v9.22.0
	github.com/shopspring/decimal v1.4.0
	golang.org/x/crypto v0.55.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.4.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
)
