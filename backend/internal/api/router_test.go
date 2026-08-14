package api

import (
	"strings"
	"testing"
)

// Every route must be a deliberate access decision.
//
// A permission route must name its permission; a public or merely-authenticated
// route must record why it is allowed to be. This is what stops QA gate M7 from
// being bypassed by forgetting rather than by deciding: adding an endpoint
// without thinking about access fails here, not in production.
func TestEveryRouteDeclaresItsAccess(t *testing.T) {
	s := &Server{}

	for _, rt := range s.Routes() {
		name := rt.Method + " " + rt.Pattern
		t.Run(name, func(t *testing.T) {
			if rt.Handler == nil {
				t.Fatal("route has no handler")
			}

			switch rt.Access {
			case AccessPermission:
				if rt.Permission == "" {
					t.Fatal("permission route names no permission")
				}
			case AccessPublic, AccessAuthenticated:
				if strings.TrimSpace(rt.Why) == "" {
					t.Fatal("route is reachable without a permission but does not " +
						"record why. Widening access must be a reviewed decision.")
				}
			case AccessSuperAdmin:
				if rt.Permission != "" {
					t.Fatal("a Super Admin route must not also name a tenant permission; " +
						"the platform plane holds none")
				}
			default:
				t.Fatalf("unknown access level %d", rt.Access)
			}
		})
	}
}

// Permission strings must match the format the database enforces
// (`<module>.<verb>`), or the grant will never match and the route becomes
// silently unreachable for everyone.
func TestPermissionNamesAreWellFormed(t *testing.T) {
	s := &Server{}

	for _, rt := range s.Routes() {
		if rt.Access != AccessPermission {
			continue
		}
		p := rt.Permission
		module, verb, ok := strings.Cut(p, ".")
		if !ok {
			t.Errorf("%s %s: permission %q is not <module>.<verb>", rt.Method, rt.Pattern, p)
			continue
		}
		if module == "" || verb == "" {
			t.Errorf("%s %s: permission %q has an empty part", rt.Method, rt.Pattern, p)
		}
		if p != strings.ToLower(p) {
			t.Errorf("%s %s: permission %q must be lower case", rt.Method, rt.Pattern, p)
		}
		if strings.Count(p, ".") != 1 {
			t.Errorf("%s %s: permission %q must have exactly one dot", rt.Method, rt.Pattern, p)
		}
	}
}

// Public routes are a small, deliberate set. If this list grows, that should be
// a conscious change reviewed here rather than a quiet addition elsewhere.
func TestPublicRoutesAreOnlyTheExpectedOnes(t *testing.T) {
	expected := map[string]bool{
		"GET /healthz":              true,
		"GET /readyz":               true,
		"GET /api/v1/meta/version":  true,
		"POST /api/v1/auth/login":   true,
		"POST /api/v1/auth/refresh": true,
	}

	s := &Server{}
	for _, rt := range s.Routes() {
		if rt.Access != AccessPublic {
			continue
		}
		key := rt.Method + " " + rt.Pattern
		if !expected[key] {
			t.Errorf("%s is public but is not in the reviewed public list. "+
				"If it should be public, add it there deliberately.", key)
		}
		delete(expected, key)
	}
	for missing := range expected {
		t.Errorf("%s was expected to be public but is not registered as such", missing)
	}
}

// No two routes may share a method and pattern; chi would panic at mount time,
// which is a startup crash rather than a test failure.
func TestNoDuplicateRoutes(t *testing.T) {
	seen := map[string]bool{}
	s := &Server{}
	for _, rt := range s.Routes() {
		key := rt.Method + " " + rt.Pattern
		if seen[key] {
			t.Errorf("duplicate route %s", key)
		}
		seen[key] = true
	}
}
