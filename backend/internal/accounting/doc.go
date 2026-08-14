// Package accounting owns the chart of accounts, the double-entry journal and
// the posting-rule engine.
//
// The two guarantees the rest of the system relies on — debits equal credits,
// and posted history is immutable — are enforced by database constraints in
// migration 0015 rather than by code here. That is deliberate: an application
// check protects only the paths that go through it, while a deferred constraint
// also stops a background job, a migration and a support script.
package accounting
