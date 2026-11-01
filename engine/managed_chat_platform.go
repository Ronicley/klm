package main

import (
	"errors"
	"runtime"
)

// Version/profile compatibility is independent of graph admission. Linux chat
// ownership is the nonroot subreaper/pidfd domain, never a graph process seal.
func managedChatPlatform() bool {
	return runtime.GOOS == "windows" || runtime.GOOS == "linux" && runtime.GOARCH == "amd64"
}
func validateManagedChatPlatform() error {
	if !managedChatPlatform() {
		return errors.New("Managed chats require Windows or Linux amd64 with working subreaper/pidfd supervision.")
	}
	return nil
}
