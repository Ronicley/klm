package main

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

func renameDirectoryNoReplace(source, destination string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return windows.MoveFile(from, to) // MoveFile never replaces an existing entry.
}

func removeEmptyDirectory(path string) error {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	err = windows.RemoveDirectory(name)
	if errors.Is(err, windows.ERROR_DIR_NOT_EMPTY) {
		return syscall.ENOTEMPTY
	}
	return err
}
