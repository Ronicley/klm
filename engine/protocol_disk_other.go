//go:build !windows && !linux

package main

func protocolFreeDisk(string) (uint64, error) { return 0, errProtocolStagingIO }
