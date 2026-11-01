//go:build !linux

package main

// Windows retains its existing Job/handle admission; other Unix platforms do not
// advertise the Linux managed-chat profile.
func runtimeGroupHealth(*runtimeProcessGroup) error       { return nil }
func watchRuntimeGroup(*runtimeProcessGroup, func(error)) {}
