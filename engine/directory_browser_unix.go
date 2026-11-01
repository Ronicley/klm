//go:build !windows

package main

const platformNativeDirectoryPicker = false

func platformDirectoryRoots() ([]filesystemDirectory, error) {
	return []filesystemDirectory{{Name: "/", Path: "/"}}, nil
}
