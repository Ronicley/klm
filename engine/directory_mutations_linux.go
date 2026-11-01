package main

import (
	"errors"

	"golang.org/x/sys/unix"
)

func renameDirectoryNoReplace(source, destination string) error {
	err := unix.Renameat2(unix.AT_FDCWD, source, unix.AT_FDCWD, destination, unix.RENAME_NOREPLACE)
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.EINVAL) {
		return errors.ErrUnsupported
	}
	return err
}

func removeEmptyDirectory(path string) error {
	return unix.Rmdir(path)
}
