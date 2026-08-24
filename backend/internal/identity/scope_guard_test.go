package identity

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every scope dimension the design declares must actually be consulted.
//
// This exists because all four of them were not. Design 04 §4.3 layers store,
// warehouse, amount and time on top of the permission verbs; the schema carries
// the columns, the resolver reads them into Grants, /auth/me reported an amount
// ceiling to the client — and outside this package nothing ever asked. The
// checks compiled, were unit-tested, and gated nothing. A branch manager could
// register a till in a branch that was not theirs; a cashier with a SAR 50
// ceiling could grant any discount at all.
//
// Nothing was mis-authorised in production only because no scoped assignment
// had been written yet: provisioning creates every assignment unscoped, so
// every actor was legitimately unlimited. The hole was latent, and would have
// opened silently on the day somebody first scoped a manager to a branch —
// which the schema, the resolver and the client all invited them to do.
//
// It is the same shape as the unmounted shift service that TestEveryFieldOfThe
// ServerIsReachable was written for: a component proved in isolation while the
// join to the rest of the system was never made. A test of Grants.InStore
// passes whether or not anything calls it.
//
// The time dimension is deliberately absent from this list: it is enforced in
// the resolve query's valid_from/valid_until predicate rather than by a Go
// call, so an expired assignment never becomes a grant in the first place.
//
// The COMPANY dimension is covered by TestTheCompanyScopeIsEnforced below
// rather than here, because it is not an identity.Check* call. It is checked by
// actor.CanAccessCompany against a claim in the token, and it had gone the
// whole way round the same loop this test guards against: the column existed,
// the resolver read it, the claim was defined, the parser read it back, and the
// checker consulted it -- but nothing ever FILLED the claim, so an empty list
// meant "every company" and the check passed for everyone. A test that only
// looked for callers would have found one and reported the dimension enforced.
func TestEveryScopeCheckIsCalled(t *testing.T) {
	checks := []string{
		"CheckStoreScope",
		"CheckWarehouseScope",
		"CheckAmountLimit",
	}

	callers := map[string][]string{}
	root := internalDir(t)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".go") {
			return err
		}
		// Test files do not count. A check called only from its own test is
		// exactly the state this guards against.
		if strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		// This package declares them; calling them here would prove nothing.
		if strings.HasPrefix(rel, "identity/") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, name := range checks {
			if strings.Contains(string(body), "identity."+name+"(") {
				callers[name] = append(callers[name], rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the source tree: %v", err)
	}

	for _, name := range checks {
		if len(callers[name]) == 0 {
			t.Errorf("identity.%s is called by nothing outside its own package.\n"+
				"A scope the design declares and no route consults is not a "+
				"restriction, it is a comment. Either wire it into the handlers "+
				"that accept the id it guards, or delete it and say in design 04 "+
				"that the dimension is not enforced.", name)
			continue
		}
		t.Logf("identity.%s: %s", name, strings.Join(callers[name], ", "))
	}
}

// internalDir finds `backend/internal` from this package's directory.
func internalDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve the internal directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "identity")); err != nil {
		t.Fatalf("expected to find the internal tree at %s: %v", dir, err)
	}
	return dir
}

// The company dimension must be both CHECKED and FED.
//
// Checking without feeding is what went wrong: actor.CanAccessCompany was
// called from four handler files and read Actor.CompanyIDs, which nothing ever
// populated. An empty list means "every company in the tenant", so a user
// confined to one company could act in another and the check reported success.
//
// So this asserts both halves. Either one alone is a dimension that looks
// enforced and is not.
func TestTheCompanyScopeIsEnforced(t *testing.T) {
	root := internalDir(t)

	var checkedIn, fedIn []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".go") {
			return err
		}
		if strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)

		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(body)

		// Checked: somebody asks whether this actor may touch a company.
		if strings.Contains(text, "CanAccessCompany(") &&
			!strings.HasPrefix(rel, "platform/actor/") {
			checkedIn = append(checkedIn, rel)
		}
		// Fed: somebody puts the answer into the actor in the first place.
		if strings.Contains(text, "CompanyIDs = ") || strings.Contains(text, "CompanyIDs:") {
			fedIn = append(fedIn, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the source tree: %v", err)
	}

	if len(checkedIn) == 0 {
		t.Error("nothing calls actor.CanAccessCompany. A tenant with several " +
			"companies has no separation between their books.")
	}
	if len(fedIn) == 0 {
		t.Error("nothing ever sets Actor.CompanyIDs, so CanAccessCompany reads " +
			"an empty list, which means every company in the tenant. The check " +
			"is called and passes for everyone -- which is worse than not " +
			"having it, because it reads as enforced.")
	}
	t.Logf("company scope checked in: %s", strings.Join(checkedIn, ", "))
	t.Logf("company scope fed in:     %s", strings.Join(fedIn, ", "))
}
