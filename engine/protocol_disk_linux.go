//go:build linux

package main

import "golang.org/x/sys/unix"

func protocolFreeDisk(dir string) (uint64, error) {
	var stat unix.Statfs_t
	err := unix.Statfs(dir, &stat)
	return stat.Bavail * uint64(stat.Bsize), err
}
