//go:build !linux && !windows

package main

import (
	"errors"
	"syscall"
)

func renameDirectoryNoReplace(source, destination string) error {
	return errors.ErrUnsupported
}

func removeEmptyDirectory(path string) error {
	return syscall.Rmdir(path)
}
