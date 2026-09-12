package jobs

import (
	"fmt"
	"testing"

	"github.com/mahedi-emon/Biz1core/backend/internal/identity"
)

// TestMailPreview prints the transactional messages so they can be READ.
//
// Not an assertion -- the assertions are elsewhere. This exists because a mail
// is a thing somebody opens, and reviewing the format string that builds one
// is not the same as looking at what lands in the inbox. Run with:
//
//	go test ./internal/jobs/ -run TestMailPreview -v
func TestMailPreview(t *testing.T) {
	for _, m := range []struct {
		what    string
		subject string
		body    string
	}{
		func() (r struct {
			what, subject, body string
		}) {
			s, b := passwordResetMessage(identity.NotifyPayload{
				FullName: "Noor Owner", Code: "483920", ExpiresInMinutes: 15,
			})
			return struct{ what, subject, body string }{"password reset", s, b}
		}(),
		func() (r struct {
			what, subject, body string
		}) {
			s, b := ownerInvitationMessage(identity.NotifyPayload{
				FullName: "Noor Owner", BusinessName: "Noor Retail",
				PlanTier: "starter", Email: "owner@noor.test",
				LoginURL: "https://shop.example.com/login", PlanUntil: "2027-09-12",
			})
			return struct{ what, subject, body string }{"owner invitation", s, b}
		}(),
	} {
		fmt.Printf("\n===== %s =====\nSubject: %s\n\n%s\n", m.what, m.subject, m.body)
	}
}
