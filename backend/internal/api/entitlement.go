// Enforcing what a subscription includes (blueprint H5).
//
// # The gate that was written and never asked
//
// `billing.Allows` resolves a tenant's entitlement correctly — plan default,
// tenant override on top, expiry respected, unknown feature failing closed —
// and its own comment says it is meant to be asked "on the request path in
// front of a handler". Nothing asked it. `Entitlements` merely reported, so
// plan tiers gated nothing: a starter tenant, whose plan excludes payroll,
// loyalty, analytics, approvals, assets, warranty, instalments, API keys and
// webhooks, could reach every one of them.
//
// # Middleware, not a line in each handler
//
// A check repeated in forty handlers is a check thirty-nine of them can be
// written without. The route table already carries each route's permission as
// data; the feature belongs beside it for the same reason, and the guard test
// below the table proves every feature named is one the plans actually sell.
//
// # 402, not 403
//
// The caller is authenticated and holds the permission. What is missing is
// commercial, and the remedies differ completely: a 403 sends somebody to their
// owner to ask for a permission, a 402 sends them to their subscription. A
// client that could not tell them apart would send people to the wrong place.
package api

import (
	"net/http"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
)

// requireFeature refuses a request whose tenant's plan excludes the module.
func (s *Server) requireFeature(feature string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a := actor.From(r.Context())

			// The platform operator is not a subscriber and has no plan of its
			// own. Gating the control plane on a tenant's entitlement would be
			// asking the wrong question of the wrong party.
			if a.IsSuperAdmin || a.TenantID.String() == "" {
				next.ServeHTTP(w, r)
				return
			}

			allowed, err := s.billing.Permits(r.Context(), a.TenantID, feature)
			if err != nil {
				httpx.Error(w, r, err)
				return
			}
			if !allowed {
				httpx.Error(w, r, errs.Newf(errs.CodeFeatureNotInPlan,
					"Your plan does not include %s. An owner can change the "+
						"subscription, or ask RawSyst to enable it.",
					featureLabel(feature)))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// featureLabel is the module's name in a sentence.
//
// The seeded keys are identifiers; a person reading a refusal needs the words
// they would use for the thing they were trying to do.
func featureLabel(feature string) string {
	switch feature {
	case "payroll":
		return "payroll"
	case "loyalty":
		return "loyalty and gift cards"
	case "promotions":
		return "promotions"
	case "installments":
		return "instalment plans"
	case "warranty":
		return "warranty and service"
	case "assets":
		return "fixed assets"
	case "analytics":
		return "analytics"
	case "approvals":
		return "the approval centre"
	case "api_access":
		return "API access"
	case "webhooks":
		return "webhooks"
	case "consolidation":
		return "group consolidation"
	case "einvoicing":
		return "e-invoicing"
	case "label_studio":
		return "the label studio"
	case "online_orders":
		return "online orders and delivery"
	default:
		return feature
	}
}

// featureOfRoute maps a route family to the H5 module a plan sells.
//
// # Why a table rather than a field on Route
//
// The route table is written as positional literals — four hundred and
// twenty-six of them — so a seventh field would mean touching every line to say
// nothing about most of them. A prefix table says the same thing in one place a
// reader can check against `plan_feature` at a glance.
//
// # The names come from the plans, not from here
//
// Every value below is a feature seeded in `plan_feature`. That table IS the
// commercial model; inventing a name here would give the product two answers
// about what a subscription contains. `TestEveryGatedFeatureIsOneThePlansSell`
// fails if this drifts.
//
// # What is deliberately NOT gated
//
// `wholesale` and `multi_company` are sold as features and are not route
// families. Wholesale is a customer type and a price tier — it has no endpoint
// of its own to refuse, and gating the customer routes would stop a shop
// serving retail customers too. Multi-company is a CEILING, already enforced
// where it belongs: `CommitBusinessInfo` checks `max_companies` against
// `tenant_limit` and refuses the second company on a one-company plan. Gating a
// route as well would refuse the FIRST company on every plan.
//
// The core of the product — selling, stock, customers, purchasing, accounting,
// reports, settings — is in every tier and appears nowhere below.
var featureOfRoute = map[string]string{
	"payroll":              "payroll",
	"employees":            "payroll",
	"leave":                "payroll",
	"attendance":           "payroll",
	"eosb":                 "payroll",
	"advances":             "payroll",
	"commission-rules":     "payroll",
	"loyalty":              "loyalty",
	"gift-cards":           "loyalty",
	"wallets":              "loyalty",
	"store-credit":         "loyalty",
	"promotions":           "promotions",
	"installments":         "installments",
	"service-jobs":         "warranty",
	"serials":              "warranty",
	"assets":               "assets",
	"investors":            "assets",
	"analytics":            "analytics",
	"approvals":            "approvals",
	"approval-rules":       "approvals",
	"approval-delegations": "approvals",
	"api-keys":             "api_access",
	"webhooks":             "webhooks",
	"groups":               "consolidation",
	"einvoicing":           "einvoicing",
	"labels":               "label_studio",
	"orders":               "online_orders",
	"deliveries":           "online_orders",
}

// featureFor returns the module a route pattern belongs to, if any.
func featureFor(pattern string) string {
	const prefix = "/api/v1/"
	if len(pattern) <= len(prefix) || pattern[:len(prefix)] != prefix {
		return ""
	}
	rest := pattern[len(prefix):]
	if i := indexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	return featureOfRoute[rest]
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
