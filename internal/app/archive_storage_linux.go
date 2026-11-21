package app

import "golang.org/x/sys/unix"

func filesystemAvailableBytes(path string) (uint64, error) {
	var stats unix.Statfs_t
	if err := unix.Statfs(path, &stats); err != nil {
		return 0, err
	}
	blockSize := stats.Frsize
	if blockSize == 0 {
		blockSize = stats.Bsize
	}
	return stats.Bavail * uint64(blockSize), nil
}
