// The three environment readings the archiver needs and the rest of the
// product already had.
//
// # Why the storage settings are read here and not by config.Load
//
// `archive_command` runs inside the PostgreSQL container. That container has no
// application database connection, no JWT signing secret and no data encryption
// keys, and it has no business holding any of them — it is the database, not
// the product. `config.Load` requires all three and would refuse to start,
// which in practice means every segment failing to archive with a message about
// a missing signing key: a true statement about the least relevant thing.
//
// So the storage block is read directly, from exactly the same variable names
// `config.Load` reads, with exactly the same defaults. They are the same five
// names in `.env.example`, and the compose file passes them to one more
// container than it used to.
package backup

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/build"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/config"
)

// storageFromEnv reads the object store settings, and nothing else.
func storageFromEnv() config.Storage {
	return config.Storage{
		Endpoint:        strings.TrimSpace(os.Getenv("RAWSYST_S3_ENDPOINT")),
		Region:          envOr("RAWSYST_S3_REGION", "us-east-1"),
		Bucket:          strings.TrimSpace(os.Getenv("RAWSYST_S3_BUCKET")),
		AccessKeyID:     os.Getenv("RAWSYST_S3_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("RAWSYST_S3_SECRET_ACCESS_KEY"),
		PathStyle:       envBool("RAWSYST_S3_PATH_STYLE", true),
	}
}

func envBool(name string, fallback bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(name)))
	switch v {
	case "":
		return fallback
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func buildVersion() string { return build.Version }

// parseByteSize reads `16MB`, `16MiB`, `1GB` or a plain number of bytes.
//
// PostgreSQL prints `wal_segment_size` in the first form and an operator writes
// it in the second, so both are accepted. A value this cannot read is an error
// rather than a silent fallback: an archive whose gap arithmetic quietly
// reverted to 16 MiB on a cluster initialised with 64 MiB would report a gap
// between every pair of adjacent segments.
// ParseByteSize is `parseByteSize` for callers outside this package — the
// command line, which lets an operator say what geometry their cluster was
// initialised with.
func ParseByteSize(s string) (int64, error) { return parseByteSize(s) }

func parseByteSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, fmt.Errorf("empty size")
	}
	mult := int64(1)
	for _, unit := range []struct {
		suffix string
		factor int64
	}{
		{"kib", 1 << 10}, {"mib", 1 << 20}, {"gib", 1 << 30},
		{"kb", 1 << 10}, {"mb", 1 << 20}, {"gb", 1 << 30},
		{"k", 1 << 10}, {"m", 1 << 20}, {"g", 1 << 30},
		{"b", 1},
	} {
		if rest, ok := strings.CutSuffix(s, unit.suffix); ok {
			s, mult = strings.TrimSpace(rest), unit.factor
			break
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a size", s)
	}
	return n * mult, nil
}
