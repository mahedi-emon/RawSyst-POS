package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/config"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Claims is the access-token payload.
//
// It deliberately does NOT carry permissions. Those are resolved server-side on
// every request, so a permission revoked at 10:00 stops working at 10:00 rather
// than when a 15-minute token happens to expire. For a system handling money
// that window is unacceptable, and the cost is one cached lookup per request.
type Claims struct {
	jwt.RegisteredClaims

	TenantID     string   `json:"tid,omitempty"` // empty for a platform Super Admin
	SessionID    string   `json:"sid"`
	IsSuperAdmin bool     `json:"sa,omitempty"`
	CompanyIDs   []string `json:"cid,omitempty"` // empty = every company in the tenant

	// DeviceID is set only for POS terminal tokens, which are scoped to the
	// sync endpoints and cannot act as a person.
	DeviceID string `json:"did,omitempty"`
}

// TokenService issues and verifies tokens.
type TokenService struct {
	secret     []byte
	issuer     string
	accessTTL  time.Duration
	refreshTTL time.Duration
}

func NewTokenService(cfg config.Auth) *TokenService {
	return &TokenService{
		secret:     cfg.JWTSecret,
		issuer:     cfg.Issuer,
		accessTTL:  cfg.AccessTokenTTL,
		refreshTTL: cfg.RefreshTokenTTL,
	}
}

// IssueAccess mints a signed access token for a user session.
func (s *TokenService) IssueAccess(a actor.Actor) (string, time.Time, error) {
	now := time.Now().UTC()
	exp := now.Add(s.accessTTL)

	c := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.issuer,
			Subject:   a.UserID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			ID:        uuid.NewString(),
		},
		SessionID:    a.SessionID.String(),
		IsSuperAdmin: a.IsSuperAdmin,
	}
	if a.TenantID != uuid.Nil {
		c.TenantID = a.TenantID.String()
	}
	for _, id := range a.CompanyIDs {
		c.CompanyIDs = append(c.CompanyIDs, id.String())
	}
	if a.DeviceID != uuid.Nil {
		c.DeviceID = a.DeviceID.String()
	}

	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(s.secret)
	if err != nil {
		return "", time.Time{}, errs.Wrap(err, errs.CodeInternal, "Could not issue a session token.")
	}
	return tok, exp, nil
}

// Verify parses and validates an access token, returning the Actor it
// describes.
//
// The signing method is pinned to HMAC. Accepting whatever the token's header
// declares is the classic JWT "alg: none" and RS256-to-HS256 confusion attack,
// where an attacker signs a token with the public key as an HMAC secret.
func (s *TokenService) Verify(raw string) (actor.Actor, error) {
	var c Claims
	tok, err := jwt.ParseWithClaims(raw, &c, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return s.secret, nil
	},
		jwt.WithIssuer(s.issuer),
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
	)
	if err != nil || !tok.Valid {
		return actor.Actor{}, errs.Wrap(err, errs.CodeUnauthenticated,
			"Your session has expired or is not valid. Please sign in again.")
	}

	a := actor.Actor{IsSuperAdmin: c.IsSuperAdmin}

	if a.UserID, err = uuid.Parse(c.Subject); err != nil {
		return actor.Actor{}, errs.Wrap(err, errs.CodeUnauthenticated, "Your session is not valid.")
	}
	if a.SessionID, err = uuid.Parse(c.SessionID); err != nil {
		return actor.Actor{}, errs.Wrap(err, errs.CodeUnauthenticated, "Your session is not valid.")
	}
	if c.TenantID != "" {
		if a.TenantID, err = uuid.Parse(c.TenantID); err != nil {
			return actor.Actor{}, errs.Wrap(err, errs.CodeUnauthenticated, "Your session is not valid.")
		}
	}
	if c.DeviceID != "" {
		if a.DeviceID, err = uuid.Parse(c.DeviceID); err != nil {
			return actor.Actor{}, errs.Wrap(err, errs.CodeUnauthenticated, "Your session is not valid.")
		}
	}
	for _, raw := range c.CompanyIDs {
		id, parseErr := uuid.Parse(raw)
		if parseErr != nil {
			return actor.Actor{}, errs.Wrap(parseErr, errs.CodeUnauthenticated, "Your session is not valid.")
		}
		a.CompanyIDs = append(a.CompanyIDs, id)
	}

	// A token claiming both Super Admin and a tenant is contradictory: the
	// platform plane belongs to no tenant. Refuse rather than guess, because
	// guessing either widens or narrows access silently.
	if a.IsSuperAdmin && a.TenantID != uuid.Nil {
		return actor.Actor{}, errs.New(errs.CodeUnauthenticated, "Your session is not valid.")
	}
	if !a.IsSuperAdmin && a.TenantID == uuid.Nil {
		return actor.Actor{}, errs.New(errs.CodeUnauthenticated, "Your session is not valid.")
	}

	return a, nil
}

// --- refresh tokens ----------------------------------------------------

// Refresh tokens are opaque random values, not JWTs. A JWT refresh token cannot
// be revoked before it expires without server state anyway, so the state is the
// point: only the hash is stored, so a leaked database backup yields nothing
// usable.
const refreshTokenBytes = 32

// NewRefreshToken returns a token to give the client and the hash to store.
func NewRefreshToken() (token, hash string, err error) {
	buf := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", errs.Wrap(err, errs.CodeInternal, "Could not issue a session token.")
	}
	token = base64.RawURLEncoding.EncodeToString(buf)
	return token, HashRefreshToken(token), nil
}

// HashRefreshToken hashes a refresh token for storage and lookup.
//
// Plain SHA-256 is correct here, unlike for passwords: the token is 256 bits of
// uniform randomness, so there is no dictionary to attack and no benefit from a
// slow KDF — which would only add latency to every refresh.
func HashRefreshToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *TokenService) RefreshTTL() time.Duration { return s.refreshTTL }
func (s *TokenService) AccessTTL() time.Duration  { return s.accessTTL }
