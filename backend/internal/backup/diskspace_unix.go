//go:build !windows

package backup

import "golang.org/x/sys/unix"

// freeBytes is what an unprivileged process may still write at path.
//
// `Bavail` rather than `Bfree`: the difference is the reserve the filesystem
// keeps for root, and a backup does not run as root. Counting it would let this
// check pass and the dump then fail with no space left.
func freeBytes(path string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
