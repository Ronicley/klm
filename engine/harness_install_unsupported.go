//go:build !windows && !linux

package main

import (
	"context"
	"errors"
)

func conventionalNodePath() string { return "/usr/local/bin/node" }
func knownNodePaths() []string     { return []string{conventionalNodePath()} }
func conventionalNPMPath() string  { return "/usr/local/lib/node_modules/npm/bin/npm-cli.js" }
func defaultGlobalPrefix() string  { return "/usr/local" }
func harnessInstallPlatform() string {
	return "Global harness installation supports Windows amd64 and glibc Linux amd64 only."
}
func bootstrapPermission() error                { return errors.New(harnessInstallPlatform()) }
func publishNode(context.Context, string) error { return bootstrapPermission() }
