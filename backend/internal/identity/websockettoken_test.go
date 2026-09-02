package identity

import (
	"net/http"
	"testing"
)

// A browser cannot set a header on a WebSocket, so the token arrives as a
// subprotocol. These are the ways that goes wrong.

func request(upgrade, protocols string) *http.Request {
	r, _ := http.NewRequest(http.MethodGet, "/api/v1/live", nil)
	if upgrade != "" {
		r.Header.Set("Upgrade", upgrade)
	}
	if protocols != "" {
		r.Header.Set("Sec-WebSocket-Protocol", protocols)
	}
	return r
}

func TestATokenOfferedAsASubprotocolIsFound(t *testing.T) {
	got := bearerToken(request("websocket", "rawsyst.auth, a-token"))
	if got != "a-token" {
		t.Fatalf("token = %q, want a-token", got)
	}

	// Browsers vary on the spacing, and some libraries send no space at all.
	if got := bearerToken(request("WebSocket", "rawsyst.auth,a-token")); got != "a-token" {
		t.Fatalf("token = %q with no space, want a-token", got)
	}
}

// TestAnOrdinaryRequestCannotAuthenticateThroughASubprotocolHeader is the one
// that matters.
//
// The header is trivially settable by any HTTP client. If it were honoured on
// a normal request, every route in the product would have a second way in that
// nothing else in this file knows about — so it is read ONLY when the request
// is actually an upgrade.
func TestAnOrdinaryRequestCannotAuthenticateThroughASubprotocolHeader(t *testing.T) {
	if got := bearerToken(request("", "rawsyst.auth, a-token")); got != "" {
		t.Fatalf("a plain request authenticated through a subprotocol: %q", got)
	}
	if got := bearerToken(request("h2c", "rawsyst.auth, a-token")); got != "" {
		t.Fatalf("a non-websocket upgrade authenticated: %q", got)
	}
}

func TestAnUpgradeWithoutTheMarkerCarriesNoToken(t *testing.T) {
	for _, offered := range []string{
		"",
		"something.else",
		// The marker with nothing after it: a client that offered the marker
		// and forgot the token must not be read as offering the marker AS the
		// token.
		"rawsyst.auth",
		"chat, rawsyst.auth",
	} {
		if got := bearerToken(request("websocket", offered)); got != "" {
			t.Fatalf("protocols %q yielded a token %q", offered, got)
		}
	}
}

// TestTheHeaderStillWinsWhenBothArePresent: a native client CAN set
// Authorization, and when it does that is what is used.
func TestTheHeaderStillWinsWhenBothArePresent(t *testing.T) {
	r := request("websocket", "rawsyst.auth, from-subprotocol")
	r.Header.Set("Authorization", "Bearer from-header")
	if got := bearerToken(r); got != "from-header" {
		t.Fatalf("token = %q, want from-header", got)
	}
}
