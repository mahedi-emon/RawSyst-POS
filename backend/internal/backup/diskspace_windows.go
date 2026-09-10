//go:build windows

package backup

import "golang.org/x/sys/windows"

// freeBytes is what this process may still write at path.
//
// Windows is a development platform for this product and not a deployment one,
// but the check has to compile and answer here or it would be dead code that
// only runs where nobody can try it.
func freeBytes(path string) (uint64, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var availableToCaller, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(
		p, &availableToCaller, &total, &free,
	); err != nil {
		return 0, err
	}
	return availableToCaller, nil
}
