//go:build integration

// Database-backed tests proving the guarantees the rest of the system assumes.
//
// These are not "nice to have" coverage. Each one corresponds to a QA gate in
// blueprint Part M that is tested by direct manipulation rather than through
// the API, precisely because an application-layer check would not catch the
// failure:
//
//	M8 — cross-tenant access via manipulated requests must fail in every case
//	M1 — closed periods truly locked; audit history append-only
//
// Run with: make test-db   (needs RAWSYST_DB_DSN pointing at a test database)
package db

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/config"
)

func testPool(t *testing.T) *Pool {
	t.Helper()
	dsn := os.Getenv("RAWSYST_DB_DSN")
	if dsn == "" {
		t.Skip("RAWSYST_DB_DSN not set; skipping database-backed test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := Open(ctx, config.DB{DSN: dsn, MaxConns: 8, MinConns: 1,
		MaxConnLifetime: time.Hour, MaxConnIdleTime: 30 * time.Minute})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := pool.Migrate(ctx, nil); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	return pool
}

// seedTenant provisions a tenant with one company, through the platform
// control plane. Tenant creation is a Super Admin operation by design: with
// FORCE ROW LEVEL SECURITY there is no tenant context yet, so no tenant-scoped
// path could ever create the first row.
func seedTenant(t *testing.T, pool *Pool, name string) (tenantID, companyID uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	err := pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`INSERT INTO tenant (name) VALUES ($1) RETURNING id`, name).Scan(&tenantID); err != nil {
			return err
		}
		return tx.QueryRow(ctx,
			`INSERT INTO company (tenant_id, legal_name, country, base_currency)
			 VALUES ($1, $2, 'sa', 'SAR') RETURNING id`,
			tenantID, name+" Trading Co").Scan(&companyID)
	})
	if err != nil {
		t.Fatalf("provision tenant %q: %v", name, err)
	}

	t.Cleanup(func() {
		_ = pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
			_, err := tx.Exec(context.Background(), `DELETE FROM tenant WHERE id = $1`, tenantID)
			return err
		})
	})
	return tenantID, companyID
}

func ctxAs(tenantID uuid.UUID) context.Context {
	return actor.Into(context.Background(), actor.Actor{
		UserID:   uuid.New(),
		TenantID: tenantID,
	})
}

func ctxAsPlatform() context.Context {
	return actor.Into(context.Background(), actor.Actor{
		UserID:       uuid.New(),
		IsSuperAdmin: true,
	})
}

// The assumption every other test in this file rests on: the connection cannot
// bypass row-level security.
//
// A PostgreSQL superuser ignores RLS entirely, and FORCE ROW LEVEL SECURITY
// does not change that — FORCE only covers the table owner. So an application
// connecting as a superuser has no tenant isolation at all, silently, with
// every policy still sitting in the catalogue looking correct.
//
// This is not hypothetical. CI ran for its first several days against a
// superuser, because the official postgres image creates POSTGRES_USER as one,
// and every isolation test failed there while passing locally. The tests were
// right; the environment was wrong. This one makes that impossible to miss
// again: it names the cause instead of leaving six confusing failures.
func TestConnectionCannotBypassRowLevelSecurity(t *testing.T) {
	pool := testPool(t)

	var role string
	var isSuper, canBypass bool
	err := pool.Raw().QueryRow(context.Background(), `
		SELECT rolname, rolsuper, rolbypassrls
		FROM pg_roles WHERE rolname = current_user`).
		Scan(&role, &isSuper, &canBypass)
	if err != nil {
		t.Fatalf("read current role: %v", err)
	}

	if isSuper {
		t.Fatalf("the application connects as %q, which is a PostgreSQL "+
			"SUPERUSER. Superusers ignore row-level security completely, so this "+
			"deployment has no tenant isolation regardless of what the policies "+
			"say. Create a role with NOSUPERUSER NOBYPASSRLS and connect as that.",
			role)
	}
	if canBypass {
		t.Fatalf("the application connects as %q, which holds BYPASSRLS. Every "+
			"tenant policy is inert for this connection. Remove it with "+
			"ALTER ROLE %s NOBYPASSRLS.", role, role)
	}
}

// QA gate M8. Tenant A must not be able to read tenant B's rows, even when the
// query carries no tenant filter at all — which is the realistic failure, since
// a developer forgetting a WHERE clause is far more likely than an attacker
// crafting one.
func TestCrossTenantReadIsImpossible(t *testing.T) {
	pool := testPool(t)

	tenantA, companyA := seedTenant(t, pool, "Alpha")
	tenantB, companyB := seedTenant(t, pool, "Beta")

	// Deliberately unfiltered: SELECT * FROM company, no WHERE clause.
	countAll := func(ctx context.Context) (int, []uuid.UUID) {
		var n int
		var ids []uuid.UUID
		err := pool.Tx(ctx, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT id FROM company`)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var id uuid.UUID
				if err := rows.Scan(&id); err != nil {
					return err
				}
				ids = append(ids, id)
				n++
			}
			return rows.Err()
		})
		if err != nil {
			t.Fatalf("query as tenant: %v", err)
		}
		return n, ids
	}

	nA, idsA := countAll(ctxAs(tenantA))
	if nA != 1 || idsA[0] != companyA {
		t.Fatalf("tenant A saw %d companies %v, want exactly its own (%s)", nA, idsA, companyA)
	}

	nB, idsB := countAll(ctxAs(tenantB))
	if nB != 1 || idsB[0] != companyB {
		t.Fatalf("tenant B saw %d companies %v, want exactly its own (%s)", nB, idsB, companyB)
	}
}

// Naming another tenant's id explicitly must also return nothing. This is the
// manipulated-request case from M8: the attacker knows the target id.
func TestCrossTenantReadByExplicitIDIsImpossible(t *testing.T) {
	pool := testPool(t)

	tenantA, _ := seedTenant(t, pool, "Alpha")
	_, companyB := seedTenant(t, pool, "Beta")

	var found int
	err := pool.Tx(ctxAs(tenantA), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(),
			`SELECT count(*) FROM company WHERE id = $1`, companyB).Scan(&found)
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if found != 0 {
		t.Fatalf("tenant A could see tenant B's company by id; RLS is not enforcing")
	}
}

// Writing into another tenant is refused too. RLS without a WITH CHECK clause
// would permit the insert and only hide it afterwards, which would corrupt
// another tenant's data silently.
func TestCrossTenantWriteIsRefused(t *testing.T) {
	pool := testPool(t)

	tenantA, _ := seedTenant(t, pool, "Alpha")
	tenantB, _ := seedTenant(t, pool, "Beta")

	err := pool.Tx(ctxAs(tenantA), func(tx pgx.Tx) error {
		_, execErr := tx.Exec(context.Background(),
			`INSERT INTO company (tenant_id, legal_name, country, base_currency)
			 VALUES ($1, 'Injected Co', 'sa', 'SAR')`, tenantB)
		return execErr
	})
	if err == nil {
		t.Fatal("tenant A inserted a company into tenant B; RLS WITH CHECK is missing")
	}
}

// An unauthenticated context must not reach the database at all. The zero
// Actor carries uuid.Nil, and no row carries a nil tenant, so even if the guard
// were removed the query would return nothing — but failing fast is clearer.
func TestUnauthenticatedContextIsRefused(t *testing.T) {
	pool := testPool(t)

	err := pool.Tx(context.Background(), func(tx pgx.Tx) error {
		_, execErr := tx.Exec(context.Background(), `SELECT 1`)
		return execErr
	})
	if err == nil {
		t.Fatal("an unauthenticated context was allowed to open a transaction")
	}
}

// The tenant setting must not survive into the next borrower of the same
// pooled connection. set_config's third argument being true is what guarantees
// this; the test exists because getting it wrong leaks data across requests
// under load, which is exactly the kind of bug that never appears in
// development.
func TestTenantSettingDoesNotLeakBetweenTransactions(t *testing.T) {
	pool := testPool(t)
	tenantA, _ := seedTenant(t, pool, "Alpha")

	var seen uuid.UUID
	if err := pool.Tx(ctxAs(tenantA), func(tx pgx.Tx) error {
		var err error
		seen, err = TenantIDOf(context.Background(), tx)
		return err
	}); err != nil {
		t.Fatalf("first transaction: %v", err)
	}
	if seen != tenantA {
		t.Fatalf("tenant in transaction = %s, want %s", seen, tenantA)
	}

	// A raw connection from the same pool must see no tenant setting.
	var leaked string
	if err := pool.Raw().QueryRow(context.Background(),
		`SELECT coalesce(current_setting('app.tenant_id', true), '')`).Scan(&leaked); err != nil {
		t.Fatalf("read setting on raw connection: %v", err)
	}
	if leaked != "" {
		t.Fatalf("tenant setting %q leaked outside its transaction", leaked)
	}
}

// Blueprint D4: the audit log "cannot be edited or deleted by any user,
// including Owner". Enforced by trigger, so it holds against direct SQL.
func TestAuditLogIsAppendOnly(t *testing.T) {
	pool := testPool(t)
	tenantA, _ := seedTenant(t, pool, "Alpha")
	ctx := context.Background()

	var id int64
	if err := pool.Tx(ctxAs(tenantA), func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`INSERT INTO audit_log (tenant_id, action, entity_type, after_value)
			 VALUES ($1, 'price_changed', 'product', '{"price":"100"}'::jsonb)
			 RETURNING id`, tenantA).Scan(&id)
	}); err != nil {
		t.Fatalf("insert audit row: %v", err)
	}

	// Attempted through the platform plane, which is the most privileged path
	// that exists. Blueprint D4: not even Owner or Super Admin may alter these.
	err := pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `UPDATE audit_log SET action = 'tampered' WHERE id = $1`, id)
		return e
	})
	if err == nil {
		t.Fatal("audit log row was updated; the append-only trigger is not active")
	}

	err = pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `DELETE FROM audit_log WHERE id = $1`, id)
		return e
	})
	if err == nil {
		t.Fatal("audit log row was deleted; the append-only trigger is not active")
	}
}

// Blueprint A4 draws a hard line: Super Admin controls the platform level and
// "does not interfere in the Owner's day-to-day business data." Provisioning
// tables carry the platform predicate; business tables must never gain it.
//
// This test guards that boundary. When sales, inventory and journal tables are
// added, they belong in the list below.
func TestPlatformAdminHasNoBusinessDataAccess(t *testing.T) {
	pool := testPool(t)

	// Tables Super Admin legitimately administers (migration 0006).
	platformTables := map[string]bool{
		"tenant": true, "tenant_limit": true, "company": true,
		"store": true, "device": true, "app_user": true,
		// Sessions and their refresh-token chain: revoking a session is a
		// security-incident capability (blueprint H1), and Super Admin must be
		// able to end one without the tenant's cooperation.
		"user_session": true, "session_refresh_token": true,
		"audit_log": true,
		// Setup state, not business data: which of the seven steps a new client
		// has reached. A client stuck on step 3 is a support call waiting to
		// happen, and provisioning is a platform responsibility (A5).
		"onboarding_progress": true,
		// ZATCA onboarding and CSID status per EGS unit. Blueprint A4 puts
		// failed submissions and compliance status across all tenants on the
		// platform health dashboard, which is the same reason `device` carries
		// the predicate. It exposes certificate state, never invoice content.
		"egs_unit": true,
		// Pairing a terminal, H3. The predicate is here not so Super Admin can
		// browse enrolments but because redeeming a code is made by a caller
		// with NO tenant to scope by — a terminal being paired has no
		// credential yet, so the lookup runs on the platform plane. A row holds
		// a selector, a hash and an expiry; it never holds a usable code.
		"device_enrolment": true,
		// Sync queue depth, which A4 names explicitly for the platform health
		// dashboard. Both carry counts and timings only — sync_item, which holds
		// the actual payload, is deliberately tenant-only (migration 0017).
		"sync_batch":         true,
		"device_sync_cursor": true,
	}

	var offenders []string
	err := pool.Raw().QueryRow(context.Background(), `
		SELECT coalesce(string_agg(tablename, ', ' ORDER BY tablename), '')
		FROM pg_policies
		WHERE schemaname = 'public'
		  AND qual LIKE '%is_platform_admin%'`).Scan(new(string))
	if err != nil {
		t.Fatalf("inspect policies: %v", err)
	}

	rows, err := pool.Raw().Query(context.Background(), `
		SELECT tablename FROM pg_policies
		WHERE schemaname = 'public' AND qual LIKE '%is_platform_admin%'`)
	if err != nil {
		t.Fatalf("inspect policies: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatalf("scan policy: %v", err)
		}
		if !platformTables[table] {
			offenders = append(offenders, table)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate policies: %v", err)
	}

	if len(offenders) > 0 {
		t.Fatalf("these tables grant Super Admin access but are not platform "+
			"administration tables: %s. Business data must stay tenant-only.",
			strings.Join(offenders, ", "))
	}
}

// The platform may see how deep a device's sync queue is, and never what is in
// it. Blueprint A4 asks for queue depth on the health dashboard; sync_item
// carries the actual invoice payload, so the two had to be separated.
//
// Migration 0016 granted the platform predicate to all three sync tables and
// the boundary test above caught it. This is the narrower guarantee.
func TestPlatformSeesSyncDepthButNotPayloads(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	tenantA, companyA := seedTenant(t, pool, "Alpha")

	var storeID, unitID, deviceID uuid.UUID
	if err := pool.Tx(ctxAs(tenantA), func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO store (tenant_id, company_id, code, name)
			VALUES ($1,$2,'MAIN','Main') RETURNING id`,
			tenantA, companyA).Scan(&storeID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO egs_unit (tenant_id, company_id, store_id, label, architecture)
			VALUES ($1,$2,$3,'till','smart_pos') RETURNING id`,
			tenantA, companyA, storeID).Scan(&unitID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO device (tenant_id, company_id, store_id, terminal_label, status)
			VALUES ($1,$2,$3,'Till','active') RETURNING id`,
			tenantA, companyA, storeID).Scan(&deviceID); err != nil {
			return err
		}

		var batchID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO sync_batch (tenant_id, device_id, idempotency_key, item_count)
			VALUES ($1,$2,'k',1) RETURNING id`,
			tenantA, deviceID).Scan(&batchID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO device_sync_cursor (device_id, tenant_id, last_applied_seq, highest_seen_seq)
			VALUES ($1,$2,0,1)`, deviceID, tenantA); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO sync_item
			  (tenant_id, batch_id, device_id, seq, entity_uuid, entity_type, payload, state)
			VALUES ($1,$2,$3,1,$4,'sales_invoice',
			        '{"customer":"Abdullah Trading","total":"48290.00"}'::jsonb,'pending')`,
			tenantA, batchID, deviceID, uuid.New())
		return err
	}); err != nil {
		t.Fatalf("seed sync state: %v", err)
	}

	// The platform sees the depth.
	var depth int
	if err := pool.Tx(ctxAsPlatform(), func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM device_sync_cursor WHERE device_id = $1`,
			deviceID).Scan(&depth)
	}); err != nil {
		t.Fatalf("read cursor as platform: %v", err)
	}
	if depth != 1 {
		t.Fatal("the platform cannot see sync queue depth, which A4 puts on the " +
			"health dashboard")
	}

	// And nothing of what was sold.
	var payloads int
	if err := pool.Tx(ctxAsPlatform(), func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM sync_item WHERE device_id = $1`, deviceID).Scan(&payloads)
	}); err != nil {
		t.Fatalf("read sync_item as platform: %v", err)
	}
	if payloads != 0 {
		t.Fatal("the platform operator can read queued invoice payloads; A4 says " +
			"Super Admin does not interfere in the Owner's business data")
	}

	// The tenant still sees its own, or the queue would be invisible to the
	// person who actually needs to act on it.
	if err := pool.Tx(ctxAs(tenantA), func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM sync_item WHERE device_id = $1`, deviceID).Scan(&payloads)
	}); err != nil {
		t.Fatalf("read sync_item as tenant: %v", err)
	}
	if payloads != 1 {
		t.Fatalf("the tenant sees %d of its own sync items, want 1", payloads)
	}
}

// Role definitions belong to the tenant, not the platform. Verifies the
// deliberate omission documented at the end of migration 0006.
func TestPlatformAdminCannotSeeTenantRoles(t *testing.T) {
	pool := testPool(t)
	tenantA, _ := seedTenant(t, pool, "Alpha")
	ctx := context.Background()

	var roleID uuid.UUID
	if err := pool.Tx(ctxAs(tenantA), func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`INSERT INTO role (tenant_id, key, name) VALUES ($1, 'floor_lead', 'Floor Lead')
			 RETURNING id`, tenantA).Scan(&roleID)
	}); err != nil {
		t.Fatalf("create tenant role: %v", err)
	}

	var visible int
	if err := pool.Tx(ctxAsPlatform(), func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM role WHERE id = $1`, roleID).Scan(&visible)
	}); err != nil {
		t.Fatalf("query as platform: %v", err)
	}
	if visible != 0 {
		t.Fatal("Super Admin can read a tenant's own role definitions; " +
			"role is tenant-only by design")
	}
}

// Blueprint E8.1: two rules for the same key must never cover overlapping
// dates, or "which rate applied on this day" has more than one answer. The
// GiST exclusion constraint makes that state unrepresentable.
func TestRegulatoryRulesCannotOverlap(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	const key = "SA.TEST.OVERLAP_GUARD"
	t.Cleanup(func() {
		// Deletion is blocked by trigger, so drop the guard for cleanup only.
		_, _ = pool.Raw().Exec(context.Background(),
			`ALTER TABLE regulatory_rule DISABLE TRIGGER regulatory_rule_no_delete`)
		_, _ = pool.Raw().Exec(context.Background(),
			`DELETE FROM regulatory_rule WHERE rule_key = $1`, key)
		_, _ = pool.Raw().Exec(context.Background(),
			`ALTER TABLE regulatory_rule ENABLE TRIGGER regulatory_rule_no_delete`)
	})

	insert := func(from, to string) error {
		var toArg any
		if to != "" {
			toArg = to
		}
		_, err := pool.Raw().Exec(ctx,
			`INSERT INTO regulatory_rule
			   (rule_key, country, payload, effective_from, effective_to,
			    source_authority, source_document)
			 VALUES ($1, 'sa', '{"rate":"0.15"}'::jsonb, $2, $3, 'zatca', 'test')`,
			key, from, toArg)
		return err
	}

	if err := insert("2026-01-01", "2026-07-01"); err != nil {
		t.Fatalf("first rule should insert cleanly: %v", err)
	}
	// Overlaps the first by one month.
	if err := insert("2026-06-01", "2026-12-01"); err == nil {
		t.Fatal("overlapping regulatory rules were accepted; the exclusion constraint is missing")
	}
	// Abutting ranges are fine: daterange is [) so 2026-07-01 does not overlap.
	if err := insert("2026-07-01", "2026-12-01"); err != nil {
		t.Fatalf("abutting rule should insert cleanly: %v", err)
	}
}

// A verified rule must actually be verifiable: it needs a real source document
// and no placeholder left in its payload.
//
// This test previously asserted that NO seeded rule was verified, which was
// right while every value was a placeholder. Real verification against ZATCA
// primary sources has since happened (migration 0012), so the useful guard is
// no longer "nothing is verified" — it is "nothing CLAIMS to be verified
// without the evidence". Stamping verified_on to silence a warning is the
// failure this now catches.
func TestVerifiedRulesCarryTheirEvidence(t *testing.T) {
	pool := testPool(t)

	rows, err := pool.Raw().Query(context.Background(), `
		SELECT rule_key, source_document, payload::text
		FROM regulatory_rule
		WHERE verified_on IS NOT NULL`)
	if err != nil {
		t.Fatalf("read verified rules: %v", err)
	}
	defer rows.Close()

	checked := 0
	for rows.Next() {
		var key, sourceDoc, payload string
		if err := rows.Scan(&key, &sourceDoc, &payload); err != nil {
			t.Fatalf("scan: %v", err)
		}
		checked++

		// A source document naming a version and section is what makes a claim
		// checkable a year later. "ZATCA website" is not.
		if len(sourceDoc) < 20 {
			t.Errorf("%s is marked verified but its source is only %q — cite the "+
				"document, version and section", key, sourceDoc)
		}
		if strings.Contains(payload, "__VERIFY__") {
			t.Errorf("%s is marked verified but its payload still contains a "+
				"__VERIFY__ placeholder", key)
		}
		if strings.Contains(strings.ToUpper(payload), "VERIFY AGAINST") {
			t.Errorf("%s is marked verified but its payload still says it needs "+
				"verifying", key)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	if checked == 0 {
		t.Skip("no rules are marked verified yet")
	}
}

// An unverified rule must be honestly marked: no verified_on, and its
// placeholder still visible. The danger is the opposite of the test above —
// a value that looks real but was never checked.
func TestUnverifiedRulesAreNotDisguised(t *testing.T) {
	pool := testPool(t)

	var unverified int
	err := pool.Raw().QueryRow(context.Background(), `
		SELECT count(*) FROM regulatory_rule
		WHERE country = 'sa' AND verified_on IS NULL`).Scan(&unverified)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if unverified == 0 {
		return // everything has been verified, which is the goal
	}

	// Every still-unverified rule must say so in its notes, so a reader of the
	// registry cannot mistake a starting figure for a confirmed one.
	rows, err := pool.Raw().Query(context.Background(), `
		SELECT rule_key, coalesce(notes, '') FROM regulatory_rule
		WHERE country = 'sa' AND verified_on IS NULL`)
	if err != nil {
		t.Fatalf("read unverified: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var key, notes string
		if err := rows.Scan(&key, &notes); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if strings.TrimSpace(notes) == "" {
			t.Errorf("%s is unverified but carries no note saying so; a reader "+
				"could mistake its value for a confirmed one", key)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
}

// The three release blockers named in blueprint E8.4 must be flagged.
func TestReleaseBlockersAreFlagged(t *testing.T) {
	pool := testPool(t)

	want := []string{
		"SA.ZATCA.XML_SCHEMA_VERSION", // E8.4 #3
		"SA.GOSI.RATES",               // E8.4 #2
		"SA.WPS.WAGE_FILE_FORMAT",     // E8.4 #1
	}
	for _, key := range want {
		var blocker bool
		err := pool.Raw().QueryRow(context.Background(),
			`SELECT release_blocker FROM regulatory_rule WHERE rule_key = $1`, key).Scan(&blocker)
		if err != nil {
			t.Fatalf("rule %s missing from the registry: %v", key, err)
		}
		if !blocker {
			t.Fatalf("rule %s is not flagged as a release blocker", key)
		}
	}
}

// A release blocker only blocks while it is UNVERIFIED: registry.Health reports
// `release_blocker AND verified_on IS NULL`. 0012 verified two rules in part and
// set verified_on on both, which took the whole of each off that list — including
// the halves it had not answered. 0044 gave those halves their own rules, and
// this is what stops the same thing happening again.
func TestTheUnverifiedZATCAFormatsStillBlockRelease(t *testing.T) {
	pool := testPool(t)

	want := []string{
		// SA.ZATCA.XML_CANONICALIZATION was here until 0059, which read §2.3.3
		// of the Security Features Implementation Standards v1.1 directly and
		// recorded the transform chain. Its value is now pinned by
		// TestTheCanonicalFormIsRecordedAsVerified below.
		"SA.ZATCA.UBL_FIELD_SET", // split out of XML_SCHEMA_VERSION by 0044

		// 0045. SA.ZATCA.CSR_SUBJECT_LAYOUT was here until 0060, which read the
		// OpenSSL config file out of §5.3's screenshots and found the
		// certificateTemplateName OID that 0059 recorded as its last unknown.
		// Its value is now pinned by TestTheCSRLayoutIsRecordedAsVerified below.
		//
		// This one stays: 0060 filled six of its seven __VERIFY__ placeholders,
		// and the seventh — the header carrying the OTP — is still unnamed in
		// every official document, so a CSR can now be built and still not sent.
		"SA.ZATCA.ONBOARDING_REQUEST_FORMAT",

		// 0046. The QR framing is verified; how tags 6 to 9 encode their values is
		// answered two different ways by the standard, so it stays open.
		"SA.ZATCA.QR_TAG_VALUE_ENCODING",
	}
	for _, key := range want {
		var blocker bool
		var verified *time.Time
		err := pool.Raw().QueryRow(context.Background(),
			`SELECT release_blocker, verified_on FROM regulatory_rule
			 WHERE rule_key = $1 AND effective_to IS NULL`, key).
			Scan(&blocker, &verified)
		if err != nil {
			t.Fatalf("rule %s missing from the registry: %v", key, err)
		}
		if !blocker {
			t.Errorf("rule %s is not flagged as a release blocker", key)
		}
		if verified != nil {
			t.Errorf("rule %s claims to be verified on %s; nothing in this "+
				"repository has read it from the ZATCA standard", key, verified)
		}
	}
}

// The other half of 0045: what the ZATCA documents DO publish about onboarding.
// An implementation will be built on these four values, so a silent edit to any
// of them should fail here rather than at a taxpayer's first onboarding attempt.
func TestTheVerifiedZATCAOnboardingRulesAreRecorded(t *testing.T) {
	pool := testPool(t)

	for _, key := range []string{
		"SA.ZATCA.CSR_KEY_PARAMETERS",
		"SA.ZATCA.CSR_CERTIFICATE_TEMPLATE",
		"SA.ZATCA.ONBOARDING_ENDPOINTS",
		"SA.ZATCA.ONBOARDING_OTP",
	} {
		var blocker bool
		var verified *time.Time
		err := pool.Raw().QueryRow(context.Background(),
			`SELECT release_blocker, verified_on FROM regulatory_rule
			 WHERE rule_key = $1 AND effective_to IS NULL`, key).
			Scan(&blocker, &verified)
		if err != nil {
			t.Fatalf("rule %s missing from the registry: %v", key, err)
		}
		if verified == nil {
			t.Errorf("rule %s was read from the ZATCA documents but is not "+
				"recorded as verified, so it would block release", key)
		}
		if blocker {
			t.Errorf("rule %s is verified, so it should not also block release", key)
		}
	}

	// The curve is the value most likely to be quietly "corrected" to P-256 by
	// someone reaching for crypto/elliptic, which does not carry secp256k1.
	var curve, sig string
	err := pool.Raw().QueryRow(context.Background(),
		`SELECT payload->>'curve', payload->>'csr_signature_algorithm'
		   FROM regulatory_rule
		  WHERE rule_key = 'SA.ZATCA.CSR_KEY_PARAMETERS' AND effective_to IS NULL`).
		Scan(&curve, &sig)
	if err != nil {
		t.Fatalf("read the CSR key parameters: %v", err)
	}
	if curve != "secp256k1" {
		t.Errorf("curve = %q, want secp256k1 (Technical Guideline V2 p.57)", curve)
	}
	if sig != "ecdsa-with-SHA256" {
		t.Errorf("CSR signature algorithm = %q, want ecdsa-with-SHA256", sig)
	}
}

// 0059 verified the canonical form, from the primary source.
//
// Security Features Implementation Standards v1.1 §2.3.3 prints the
// ds:Transforms element; §3 defines the previous-invoice hash as the same
// transform plus sha256. Like the QR rule this keeps release_blocker = true as
// a permanent mark of a release-critical value — the gate is
// `release_blocker AND verified_on IS NULL`.
//
// The method is pinned because two of its neighbours are what everyone reaches
// for by mistake: exclusive c14n, and the 2001 version. Either produces a
// different canonical form, so every PIH in the chain would be wrong together
// and the error would only surface at submission.
func TestTheCanonicalFormIsRecordedAsVerified(t *testing.T) {
	pool := testPool(t)

	var verified *time.Time
	var blocker bool
	var method, digest, transformAlgorithm string
	var transforms []string
	err := pool.Raw().QueryRow(context.Background(),
		`SELECT verified_on, release_blocker,
		        payload->>'method', payload->>'digest',
		        payload->>'transform_algorithm',
		        ARRAY(SELECT jsonb_array_elements_text(payload->'transforms'))
		   FROM regulatory_rule
		  WHERE rule_key = 'SA.ZATCA.XML_CANONICALIZATION' AND effective_to IS NULL`).
		Scan(&verified, &blocker, &method, &digest, &transformAlgorithm, &transforms)
	if err != nil {
		t.Fatalf("read the canonicalization rule: %v", err)
	}

	if verified == nil {
		t.Error("the canonical form was read from §2.3.3 but the rule is not " +
			"recorded as verified")
	}
	if !blocker {
		t.Error("the canonicalization rule stopped being marked release-critical")
	}
	// Literals rather than the zatca constants: that package imports this one,
	// so referencing it from here is an import cycle. The values are the same
	// ones zatca/canonical.go records, quoted from the same paragraph.
	const (
		wantMethod    = "http://www.w3.org/2006/12/xml-c14n11"
		wantTransform = "http://www.w3.org/TR/1999/REC-xpath-19991116"
		wantDigest    = "sha256"
	)
	if method != wantMethod {
		t.Errorf("method = %q, want %q — NOT exclusive c14n and not the 2001 "+
			"version", method, wantMethod)
	}
	if transformAlgorithm != wantTransform {
		t.Errorf("transform algorithm = %q, want %q", transformAlgorithm, wantTransform)
	}
	if digest != wantDigest {
		t.Errorf("digest = %q, want %q", digest, wantDigest)
	}

	// The three removals, in order. Order is part of the specification: a
	// transform chain runs in sequence and the same members in a different
	// order can produce different bytes.
	wantTransforms := []string{
		"not(//ancestor-or-self::ext:UBLExtensions)",
		"not(//ancestor-or-self::cac:Signature)",
		"not(//ancestor-or-self::cac:AdditionalDocumentReference[cbc:ID='QR'])",
	}
	if len(transforms) != len(wantTransforms) {
		t.Fatalf("%d transforms recorded, want %d",
			len(transforms), len(wantTransforms))
	}
	for i, want := range wantTransforms {
		if transforms[i] != want {
			t.Errorf("transform %d = %q, want %q", i, transforms[i], want)
		}
	}
}

// 0060 verified the CSR subject layout from the Developer Portal Manual's §5.3
// screenshots, which four earlier passes had read as empty because the section's
// substance is in images rather than in the text layer.
//
// Pinned because the two values below are the ones that cannot be recovered by
// inspection later. A CSR with a correct distinguished name and no template
// extension is refused at onboarding without saying why, and the OID that
// carries that extension went unfound through four verification passes.
func TestTheCSRLayoutIsRecordedAsVerified(t *testing.T) {
	pool := testPool(t)

	var verified *time.Time
	var templateOID, encoding, signature string
	var subjectRDNs, altNameRDNs []string
	err := pool.Raw().QueryRow(context.Background(),
		`SELECT verified_on,
		        payload->>'certificate_template_oid',
		        payload->>'certificate_template_encoding',
		        payload->>'signature_algorithm',
		        ARRAY(SELECT jsonb_array_elements_text(payload->'subject_rdns')),
		        ARRAY(SELECT jsonb_array_elements_text(payload->'alt_name_rdns'))
		   FROM regulatory_rule
		  WHERE rule_key = 'SA.ZATCA.CSR_SUBJECT_LAYOUT' AND effective_to IS NULL`).
		Scan(&verified, &templateOID, &encoding, &signature, &subjectRDNs, &altNameRDNs)
	if err != nil {
		t.Fatalf("read the CSR subject rule: %v", err)
	}

	if verified == nil {
		t.Error("the CSR layout was read from §5.3 but the rule is not " +
			"recorded as verified")
	}
	// Literals rather than the zatca constants, for the same reason as the
	// canonicalization test above: that package imports this one.
	if templateOID != "1.3.6.1.4.1.311.20.2" {
		t.Errorf("certificateTemplateName OID = %q, want 1.3.6.1.4.1.311.20.2",
			templateOID)
	}
	if encoding != "ASN1:PRINTABLESTRING" {
		t.Errorf("template encoding = %q, want ASN1:PRINTABLESTRING", encoding)
	}
	if signature != "ecdsa-with-SHA256" {
		t.Errorf("signature algorithm = %q, want ecdsa-with-SHA256", signature)
	}

	// The split between the two is the part an implementer gets wrong: the
	// manual's own sample CSR puts the serial number and the organization
	// identifier in the SUBJECT, and the config file it tells you to use puts
	// them in the alternative names.
	wantSubject := []string{"C", "OU", "O", "CN"}
	wantAltNames := []string{"SN", "UID", "title", "registeredAddress", "businessCategory"}

	if len(subjectRDNs) != len(wantSubject) {
		t.Fatalf("%d subject RDNs recorded, want %d", len(subjectRDNs), len(wantSubject))
	}
	for i, want := range wantSubject {
		if subjectRDNs[i] != want {
			t.Errorf("subject RDN %d = %q, want %q", i, subjectRDNs[i], want)
		}
	}
	if len(altNameRDNs) != len(wantAltNames) {
		t.Fatalf("%d alternative-name RDNs recorded, want %d",
			len(altNameRDNs), len(wantAltNames))
	}
	for i, want := range wantAltNames {
		if altNameRDNs[i] != want {
			t.Errorf("alternative-name RDN %d = %q, want %q", i, altNameRDNs[i], want)
		}
	}
}

// 0046 verified the QR field set and byte layout. Like XML_SCHEMA_VERSION it
// keeps release_blocker = true as a permanent mark of a release-critical value —
// the gate is `release_blocker AND verified_on IS NULL`, so the flag alone does
// not block. The layout is pinned here because getting a length or the base64
// wrapping wrong produces a QR that scans into nonsense on a printed receipt.
func TestTheQRByteLayoutIsRecordedAsVerified(t *testing.T) {
	pool := testPool(t)

	var verified *time.Time
	var blocker bool
	var tagBytes, lengthBytes, maxChars int
	var lengthCounts, separators string
	err := pool.Raw().QueryRow(context.Background(),
		`SELECT verified_on, release_blocker,
		        (payload->>'tag_bytes')::int, (payload->>'length_bytes')::int,
		        (payload->>'max_base64_chars')::int,
		        payload->>'length_counts', payload->>'separators'
		   FROM regulatory_rule
		  WHERE rule_key = 'SA.ZATCA.QR_TLV_FIELDS' AND effective_to IS NULL`).
		Scan(&verified, &blocker, &tagBytes, &lengthBytes, &maxChars,
			&lengthCounts, &separators)
	if err != nil {
		t.Fatalf("read the QR rule: %v", err)
	}

	if verified == nil {
		t.Error("the QR field set and byte layout were read from §6 of the " +
			"Technical Guideline but the rule is not recorded as verified")
	}
	if !blocker {
		t.Error("the QR rule stopped being marked release-critical")
	}
	if tagBytes != 1 || lengthBytes != 1 {
		t.Errorf("tag/length are %d/%d bytes, want 1/1", tagBytes, lengthBytes)
	}
	if maxChars != 700 {
		t.Errorf("max base64 characters = %d, want 700", maxChars)
	}
	if lengthCounts != "bytes_of_the_utf8_encoded_value" {
		t.Errorf("length_counts = %q; counting characters rather than bytes "+
			"truncates every Arabic value", lengthCounts)
	}
	if separators != "none" {
		t.Errorf("separators = %q, want none", separators)
	}

	// All nine tags, in ZATCA's order.
	for tag, want := range map[string]string{
		"1": "seller_name",
		"2": "seller_vat_registration_number",
		"3": "invoice_timestamp",
		"4": "invoice_total_including_vat",
		"5": "vat_total",
		"6": "hash_of_the_xml_invoice",
		"7": "ecdsa_signature",
		"8": "ecdsa_public_key",
		"9": "zatca_ca_signature_over_that_public_key",
	} {
		var got string
		if e := pool.Raw().QueryRow(context.Background(),
			`SELECT payload->'fields'->>$1 FROM regulatory_rule
			  WHERE rule_key = 'SA.ZATCA.QR_TLV_FIELDS' AND effective_to IS NULL`,
			tag).Scan(&got); e != nil {
			t.Fatalf("read tag %s: %v", tag, e)
		}
		if got != want {
			t.Errorf("tag %s = %q, want %q", tag, got, want)
		}
	}
}

// A VAT-registered company without a VAT number must be unrepresentable
// (blueprint E2.1: the VAT number is "a validated, required field").
func TestVATRegisteredCompanyRequiresVATNumber(t *testing.T) {
	pool := testPool(t)
	tenantA, _ := seedTenant(t, pool, "Alpha")

	err := pool.Tx(ctxAs(tenantA), func(tx pgx.Tx) error {
		_, e := tx.Exec(context.Background(),
			`INSERT INTO company (tenant_id, legal_name, country, base_currency, vat_registered)
			 VALUES ($1, 'No VAT Number Co', 'sa', 'SAR', true)`, tenantA)
		return e
	})
	if err == nil {
		t.Fatal("a VAT-registered company was created without a VAT number")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "vat_number") {
		t.Fatalf("rejected for the wrong reason: %v", err)
	}
}
