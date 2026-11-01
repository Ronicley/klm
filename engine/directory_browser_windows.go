//go:build windows

package main

import "golang.org/x/sys/windows"

const platformNativeDirectoryPicker = true

func platformDirectoryRoots() ([]filesystemDirectory, error) {
	bits, err := windows.GetLogicalDrives()
	if err != nil {
		return nil, err
	}
	roots := []filesystemDirectory{}
	// Enumerate drive bits only: probing removable/offline drives can block.
	for drive := 0; drive < 26; drive++ {
		if bits&(1<<drive) != 0 {
			path := string(rune('A'+drive)) + ":\\"
			roots = append(roots, filesystemDirectory{Name: path, Path: path})
		}
	}
	return roots, nil
}
