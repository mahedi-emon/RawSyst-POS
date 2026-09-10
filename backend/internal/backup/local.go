// A backup that is three files on somebody's laptop.
//
// # Why this exists
//
// The disaster this product has to survive is not a corrupt table. It is the
// server being gone: the disk, the provider, the account. In that case the only
// things left are what somebody downloaded — a dump, a manifest and a checksum
// — and a new machine that has never heard of the old one.
//
// Everything in this file works with no object store, no network and no
// knowledge of where the backup came from. A new server can verify an artifact
// that arrived on a USB stick and restore from it, and the old server does not
// need to exist for any of it.
//
// # The three files, and why there are three
//
//	RawSyst_Backup_<id>.dump           what pg_restore reads
//	RawSyst_Backup_<id>.manifest.json  what it is, and what it should contain
//	RawSyst_Backup_<id>.sha256         one line, in the format sha256sum reads
//
// The checksum file is redundant — the manifest carries the same hash — and it
// is there anyway, because it is the one an operator can check with a tool they
// already have and trust more than this product:
//
//	sha256sum -c RawSyst_Backup_<id>.sha256
//
// A verification somebody can run without the software being verified is worth
// more than one they cannot.
package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// ArtifactNames are the three filenames a snapshot travels as.
type ArtifactNames struct {
	Dump     string
	Manifest string
	Checksum string
}

// NamesFor is what a downloaded snapshot is called on a person's computer.
//
// The snapshot id is in every name, so three files from three different
// backups sitting in one Downloads folder cannot be mixed up, and so that a
// file found on its own a year later says what it is.
func NamesFor(id string) ArtifactNames {
	base := "RawSyst_Backup_" + id
	return ArtifactNames{
		Dump:     base + ".dump",
		Manifest: base + ".manifest.json",
		Checksum: base + ".sha256",
	}
}

// ChecksumFile is the one-line file `sha256sum -c` reads.
//
// Two spaces between the hash and the name, because that is the format, and
// getting it wrong produces a file that looks right and that `sha256sum`
// refuses.
func ChecksumFile(sum, filename string) []byte {
	return []byte(sum + "  " + filename + "\n")
}

// LocalCheck is what checking an artifact on disk found.
//
// Deliberately separate from a verification: this one can be run anywhere, in
// a second, with no database and no network, and it answers "are these three
// files consistent with each other". A verification answers "does this restore",
// which needs a Postgres. Both are useful and confusing them is not.
type LocalCheck struct {
	SnapshotID string `json:"snapshot_id"`
	Dump       string `json:"dump_path"`

	Bytes    int64  `json:"bytes"`
	SHA256   string `json:"sha256"`
	Expected string `json:"expected_sha256"`

	Encrypted      bool   `json:"encrypted"`
	KeyFingerprint string `json:"key_fingerprint,omitempty"`

	SchemaVersion int    `json:"schema_version"`
	AppVersion    string `json:"app_version,omitempty"`
	TakenAt       string `json:"taken_at,omitempty"`
	Tables        int    `json:"table_count"`
	Complete      bool   `json:"complete_inventory"`

	Findings []string `json:"findings,omitempty"`
	Passed   bool     `json:"passed"`
}

// CheckLocal reads an artifact on disk and says whether it hangs together.
//
// `manifestPath` and `checksumPath` may be empty, in which case they are looked
// for beside the dump under the names `NamesFor` produces. That is what makes
// `rawsyst backup check -dump RawSyst_Backup_X.dump` do the obvious thing.
func CheckLocal(dumpPath, manifestPath, checksumPath string) (LocalCheck, error) {
	out := LocalCheck{Dump: dumpPath}

	if manifestPath == "" {
		manifestPath = guessSibling(dumpPath, ".manifest.json")
	}
	if checksumPath == "" {
		checksumPath = guessSibling(dumpPath, ".sha256")
	}

	body, err := os.ReadFile(manifestPath)
	if err != nil {
		return out, errs.Newf(errs.CodeInvalidInput,
			"The manifest could not be read at %s. A dump without its "+
				"manifest cannot be checked and will not be restored: there "+
				"is nothing to say what it should contain.", manifestPath)
	}
	manifest, err := ParseManifest(body, "")
	if err != nil {
		return out, err
	}
	out.SnapshotID = manifest.SnapshotID
	out.Expected = manifest.Database.SHA256
	out.SchemaVersion = manifest.SchemaVersion
	out.AppVersion = manifest.AppVersion
	out.TakenAt = manifest.TakenAt
	out.Tables = manifest.Tables
	out.Complete = manifest.Complete()
	if manifest.Encryption != nil {
		out.Encrypted = true
		out.KeyFingerprint = manifest.Encryption.KeyFingerprint
	}

	f, err := os.Open(dumpPath)
	if err != nil {
		return out, errs.Newf(errs.CodeInvalidInput,
			"The dump could not be read at %s.", dumpPath)
	}
	defer f.Close()

	hash := sha256.New()
	n, err := io.Copy(hash, f)
	if err != nil {
		return out, errs.Wrap(err, errs.CodeInternal,
			"The dump could not be read to the end.")
	}
	out.Bytes = n
	out.SHA256 = hex.EncodeToString(hash.Sum(nil))

	if n != manifest.Database.Bytes {
		out.Findings = append(out.Findings, fmt.Sprintf(
			"the dump is %d bytes and the manifest says %d — a download or an "+
				"upload that did not finish looks exactly like this",
			n, manifest.Database.Bytes))
	}
	if out.SHA256 != manifest.Database.SHA256 {
		out.Findings = append(out.Findings, fmt.Sprintf(
			"the dump hashes to %s and the manifest says %s. Do not restore this",
			out.SHA256, manifest.Database.SHA256))
	}

	// The separate checksum file, where there is one. It is the same claim
	// written twice and the two disagreeing means somebody edited one of them.
	if raw, err := os.ReadFile(checksumPath); err == nil {
		stated := strings.Fields(strings.TrimSpace(string(raw)))
		if len(stated) == 0 {
			out.Findings = append(out.Findings, "the .sha256 file is empty")
		} else if !strings.EqualFold(stated[0], manifest.Database.SHA256) {
			out.Findings = append(out.Findings, fmt.Sprintf(
				"the .sha256 file says %s and the manifest says %s. These "+
					"describe the same file and one of them has been changed",
				stated[0], manifest.Database.SHA256))
		}
	}

	// What the file starts with. A sealed artifact begins with this product's
	// own magic; a custom-format dump begins with "PGDMP". Anything else is
	// not a thing pg_restore will read, and saying so now is better than
	// saying it after a restore has been started.
	head := make([]byte, 8)
	if _, err := f.ReadAt(head, 0); err == nil {
		switch {
		case IsSealed(head):
			if !out.Encrypted {
				out.Findings = append(out.Findings,
					"the dump is encrypted and the manifest does not say so")
			}
		case string(head[:5]) == "PGDMP":
			if out.Encrypted {
				out.Findings = append(out.Findings,
					"the manifest says this is encrypted and the file is a "+
						"plain dump")
			}
		default:
			out.Findings = append(out.Findings,
				"this file does not begin like a PostgreSQL custom-format "+
					"dump or an encrypted RawSyst backup")
		}
	}

	out.Passed = len(out.Findings) == 0
	return out, nil
}

func guessSibling(dumpPath, suffix string) string {
	base := strings.TrimSuffix(dumpPath, filepath.Ext(dumpPath))
	return base + suffix
}

// VerifyLocal restores an artifact on disk into a temporary database and checks
// it, exactly as a verification of a stored snapshot would.
//
// This is what a NEW server runs on a backup that arrived from a laptop, before
// anything is done with it. It needs a Postgres and an administrative
// connection and it needs nothing else — no bucket, no credentials, and no
// contact with the machine the backup came from.
func VerifyLocal(
	ctx context.Context, opts Options, adminDSN, dumpPath, manifestPath string,
) (VerifyReport, error) {
	opts = opts.withDefaults()
	started := time.Now()

	check, err := CheckLocal(dumpPath, manifestPath, "")
	if err != nil {
		return VerifyReport{}, err
	}
	report := VerifyReport{
		SnapshotID: check.SnapshotID,
		StartedAt:  started.UTC().Format(time.RFC3339),
		Bytes:      check.Bytes,
		Encrypted:  check.Encrypted,
		Complete:   check.Complete,
		Checked:    []string{"manifest", "file size", "checksum"},
	}
	if !check.Passed {
		report.Findings = check.Findings
		report.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		return report, errs.Newf(errs.CodeInvalidInput,
			"This artifact did not check out: %s",
			strings.Join(check.Findings, "; "))
	}

	if manifestPath == "" {
		manifestPath = guessSibling(dumpPath, ".manifest.json")
	}
	body, err := os.ReadFile(manifestPath)
	if err != nil {
		return report, errs.Wrap(err, errs.CodeInvalidInput,
			"The manifest could not be read.")
	}
	manifest, err := ParseManifest(body, "")
	if err != nil {
		return report, err
	}
	report.Expected = manifest.Inventory

	// Decrypted into a temporary file when it is sealed, so what reaches
	// `pg_restore` is a dump whatever the artifact was.
	plain := dumpPath
	if manifest.Encryption != nil {
		unsealed, size, err := Unseal(opts, dumpPath, manifest)
		if err != nil {
			return report, err
		}
		defer os.Remove(unsealed)
		plain, report.PlainBytes = unsealed, size
		report.Checked = append(report.Checked, "decryption and authentication")
	}

	opts.Progress(StageRestoring)
	found, err := restoreIntoScratch(ctx, opts, adminDSN, check.SnapshotID, plain)
	if err != nil {
		return report, err
	}
	report.Restored = true
	report.Found = &found
	report.Tables = found.TableCount()
	report.Schema = found.SchemaVersion
	report.Tenants = len(found.TenantRows)
	report.Companies = len(found.CompanyRows)
	for _, n := range found.Rows {
		report.Rows += n
	}
	report.Checked = append(report.Checked, "restore into a temporary database")

	opts.Progress(StageChecking)
	compare(manifest, found, &report)

	report.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	report.Took = time.Since(started).Round(time.Second).String()
	report.Passed = len(report.Findings) == 0
	if !report.Passed {
		return report, errs.Newf(errs.CodeInvalidInput,
			"This backup did not verify: %s", strings.Join(report.Findings, "; "))
	}
	return report, nil
}

// RestoreLocal puts an artifact on disk into a database that already exists and
// is empty.
//
// The same refusal as `Restore`: a target that already holds tables is not
// restored into. On a new server the sequence is create the database, restore
// into it, run the migrator, then point the application at it —
// `deploy/server/RECOVERY.md` walks it with the commands.
func RestoreLocal(
	ctx context.Context, opts Options, targetDSN, dumpPath, manifestPath string,
) error {
	opts = opts.withDefaults()

	check, err := CheckLocal(dumpPath, manifestPath, "")
	if err != nil {
		return err
	}
	if !check.Passed {
		return errs.Newf(errs.CodeInvalidInput,
			"This artifact did not check out and will not be restored: %s",
			strings.Join(check.Findings, "; "))
	}
	if err := requireEmpty(ctx, targetDSN); err != nil {
		return err
	}

	plain := dumpPath
	if check.Encrypted {
		if manifestPath == "" {
			manifestPath = guessSibling(dumpPath, ".manifest.json")
		}
		body, err := os.ReadFile(manifestPath)
		if err != nil {
			return errs.Wrap(err, errs.CodeInvalidInput,
				"The manifest could not be read.")
		}
		manifest, err := ParseManifest(body, "")
		if err != nil {
			return err
		}
		unsealed, _, err := Unseal(opts, dumpPath, manifest)
		if err != nil {
			return err
		}
		defer os.Remove(unsealed)
		plain = unsealed
	}

	if err := pgRestore(ctx, targetDSN, plain); err != nil {
		return err
	}
	// See `grantBackupRole` in restore.go: a restore creates new objects and
	// every GRANT on the old ones went with them. On a new server this is what
	// makes the FIRST backup after a recovery possible.
	return grantBackupRole(ctx, targetDSN, userOf(opts.DSN))
}
