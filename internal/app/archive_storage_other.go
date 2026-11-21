//go:build !linux && !darwin && !windows

package app

import "errors"

func filesystemAvailableBytes(path string) (uint64, error) {
	return 0, errors.ErrUnsupported
}
