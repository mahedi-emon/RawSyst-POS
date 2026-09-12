// Which backups are kept, and the rules that stop a policy emptying the store.
//
// # A retention rule is a deletion rule
//
// That is the whole of why this file is careful. Every line here is about
// removing a backup, and the failure it can produce is not "too many backups" —
// it is a business with none, discovered on the day one was needed. So the
// rules are written as guards first and arithmetic second:
//
//   - the newest completed snapshot is kept whatever the policy says;
//   - a snapshot that has been VERIFIED and is the newest verified one is kept
//     whatever the policy says, because "it ran" and "it restores" are not the
//     same claim and the second is the one worth protecting;
//   - nothing at all is deleted if no completed snapshot can be found, because
//     an empty or unreadable listing is a reason to stop rather than to start;
//   - a snapshot whose id this build cannot date is kept, because deleting
//     something because it is not understood is how a bug becomes data loss;
//   - an incomplete snapshot is deleted only when a completed one survives, so
//     a half-written backup is cleaned up without ever being the last thing
//     standing between the business and nothing.
package backup

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mahedi-emon/Biz1core/backend/internal/platform/errs"
)

// Policy is how many of each to keep.
type Policy struct {
	Daily   int
	Weekly  int
	Monthly int
}

// DefaultPolicy is a week of days, a month of weeks and a quarter of months.
//
// Fourteen, eight and twelve is the wider baseline a business-management system
// wants and it is a disk decision, not a code one: a shop keeping a year of
// monthlies is keeping twelve copies of a growing database. The default here
// is the one that fits the server this product is sized for, and every number
// is configurable — `RAWSYST_BACKUP_KEEP_DAILY` and its two siblings.
var DefaultPolicy = Policy{Daily: 7, Weekly: 4, Monthly: 3}

// Retention classes, as recorded in a manifest and shown in the history.
const (
	RetentionDaily   = "daily"
	RetentionWeekly  = "weekly"
	RetentionMonthly = "monthly"
)

// RetentionClassOf is the strongest class a snapshot taken at this time claims.
//
// Recorded at the time of the backup rather than worked out when it is
// listed, so a snapshot's class does not change under it as other snapshots
// come and go. The first backup of a month is monthly, the first of a week is
// weekly, everything else is daily — decided by the calendar, which is what
// makes it stable.
func RetentionClassOf(at time.Time) string {
	at = at.UTC()
	switch {
	case at.Day() == 1:
		return RetentionMonthly
	case at.Weekday() == time.Monday:
		return RetentionWeekly
	default:
		return RetentionDaily
	}
}

// Prune removes snapshots outside the policy.
//
// `protected` are ids that must survive whatever the arithmetic says — the
// caller passes the newest VERIFIED snapshot, which it knows and this package
// does not, because verification is recorded in the database and retention runs
// against the store.
func Prune(
	ctx context.Context, opts Options, policy Policy,
	protected []string, dryRun bool,
) ([]string, error) {
	opts = opts.withDefaults()
	snapshots, err := List(ctx, opts)
	if err != nil {
		return nil, err
	}

	var complete []Snapshot
	for _, s := range snapshots {
		if s.Completed {
			complete = append(complete, s)
		}
	}
	if len(complete) == 0 {
		return nil, errs.New(errs.CodeInvalidInput,
			"No completed snapshot is in the store. Nothing will be deleted: "+
				"an empty listing is a reason to stop rather than to start "+
				"removing backups.")
	}

	keep := whatToKeep(complete, policy)
	for _, id := range protected {
		keep[id] = true
	}

	var removed []string
	for _, s := range snapshots {
		if keep[s.ID] {
			continue
		}
		removed = append(removed, s.ID)
		if dryRun {
			continue
		}
		keys, err := opts.Store.List(ctx, opts.Prefix+"/"+s.ID+"/")
		if err != nil {
			return removed, err
		}
		// The marker first, so a delete that dies halfway leaves a snapshot
		// that is already refused by every restore rather than one that still
		// claims to be complete and is missing its dump.
		sort.Slice(keys, func(i, j int) bool {
			return keys[i] > keys[j] // COMPLETED sorts after database.dump
		})
		for _, k := range keys {
			if err := opts.Store.Delete(ctx, k); err != nil {
				return removed, err
			}
		}
	}
	sort.Strings(removed)
	return removed, nil
}

// whatToKeep decides which snapshots survive a policy.
//
// Separated from `Prune` so it can be tested without an object store: what is
// worth holding to here is the decision, not the deleting. The newest completed
// snapshot is kept unconditionally, and so is any snapshot whose id this build
// cannot date — deleting something because it is not understood is how a bug
// becomes data loss.
//
// `snapshots` is newest first, which is what `List` returns, so the first
// snapshot seen for a day, a week or a month is the one that represents it.
func whatToKeep(snapshots []Snapshot, policy Policy) map[string]bool {
	keep := map[string]bool{}
	if len(snapshots) == 0 {
		return keep
	}
	keep[snapshots[0].ID] = true

	seen := map[string]map[string]bool{"day": {}, "week": {}, "month": {}}
	limits := map[string]int{
		"day": policy.Daily, "week": policy.Weekly, "month": policy.Monthly,
	}
	for _, s := range snapshots {
		if s.TakenAt.IsZero() {
			keep[s.ID] = true
			continue
		}
		year, week := s.TakenAt.ISOWeek()
		for period, bucket := range map[string]string{
			"day":   s.TakenAt.Format("2006-01-02"),
			"week":  fmt.Sprintf("%d-W%02d", year, week),
			"month": s.TakenAt.Format("2006-01"),
		} {
			if len(seen[period]) < limits[period] && !seen[period][bucket] {
				seen[period][bucket] = true
				keep[s.ID] = true
			}
		}
	}
	return keep
}

// PolicyFromEnv reads the retention numbers from the environment.
//
// Here rather than in the command, because the agent and the command both want
// them and a policy that differed between "the button" and "the timer" would be
// a policy nobody could reason about.
func PolicyFromEnv() Policy {
	return Policy{
		Daily:   envInt("RAWSYST_BACKUP_KEEP_DAILY", DefaultPolicy.Daily),
		Weekly:  envInt("RAWSYST_BACKUP_KEEP_WEEKLY", DefaultPolicy.Weekly),
		Monthly: envInt("RAWSYST_BACKUP_KEEP_MONTHLY", DefaultPolicy.Monthly),
	}
}

// envInt reads a whole number, falling back rather than failing.
//
// A retention count that cannot be parsed falls back to the default rather
// than to zero. Zero would mean "keep none", and a typo in an environment
// variable must not be a way to delete every backup on the server.
func envInt(name string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return fallback
	}
	return n
}
