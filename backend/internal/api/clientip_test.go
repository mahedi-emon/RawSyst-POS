package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The caller's address ends up in a PostgreSQL `inet` column, which is strict.
//
// Taking everything before the last colon of RemoteAddr yields "[::1]" for an
// IPv6 client -- brackets included -- and inet refuses it. That failed the
// audit insert, which failed the sign-in, which returned 500. Every IPv6
// client, which today is most of them.
//
// It hid because a developer machine reaches the API on 127.0.0.1 and CI never
// went over IPv6. It surfaced the moment the browser was pointed at
// "localhost" instead, which resolves to ::1 first.
func TestTheClientAddressIsStorableAsInet(t *testing.T) {
	for name, c := range map[string]struct {
		remoteAddr string
		forwarded  string
		want       string
	}{
		"IPv4 with a port":   {remoteAddr: "203.0.113.7:51314", want: "203.0.113.7"},
		"IPv6 loopback":      {remoteAddr: "[::1]:54321", want: "::1"},
		"IPv6 with a port":   {remoteAddr: "[2001:db8::8a2e:370:7334]:443", want: "2001:db8::8a2e:370:7334"},
		"IPv4 with no port":  {remoteAddr: "198.51.100.9", want: "198.51.100.9"},
		"bracketed, no port": {remoteAddr: "[::1]", want: "::1"},

		// Behind Nginx, which overwrites the header.
		"forwarded, one hop":  {remoteAddr: "[::1]:1", forwarded: "203.0.113.7", want: "203.0.113.7"},
		"forwarded, a chain":  {remoteAddr: "[::1]:1", forwarded: "203.0.113.7, 198.51.100.2", want: "203.0.113.7"},
		"forwarded with room": {remoteAddr: "[::1]:1", forwarded: "  203.0.113.7 , 198.51.100.2", want: "203.0.113.7"},
		"forwarded IPv6":      {remoteAddr: "[::1]:1", forwarded: "2001:db8::1", want: "2001:db8::1"},
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
			r.RemoteAddr = c.remoteAddr
			if c.forwarded != "" {
				r.Header.Set("X-Forwarded-For", c.forwarded)
			}

			got := clientIP(r)
			if got != c.want {
				t.Errorf("clientIP = %q, want %q", got, c.want)
			}
			// The thing that actually broke: brackets reach the database.
			for _, ch := range got {
				if ch == '[' || ch == ']' {
					t.Errorf("clientIP returned %q, which PostgreSQL's inet type "+
						"refuses -- the sign-in would 500", got)
					break
				}
			}
		})
	}
}
