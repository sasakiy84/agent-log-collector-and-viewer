package app

import (
	"strings"

	"golang.org/x/sys/windows"
)

func filesystemAvailableBytes(path string) (uint64, error) {
	// GetDiskFreeSpaceEx requires a trailing separator for UNC names.
	dir, err := windows.UTF16PtrFromString(strings.TrimRight(path, `\/`) + `\`)
	if err != nil {
		return 0, err
	}
	var available uint64
	err = windows.GetDiskFreeSpaceEx(dir, &available, nil, nil)
	return available, err
}
