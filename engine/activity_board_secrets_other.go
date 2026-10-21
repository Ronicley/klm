//go:build !windows

package main

import "errors"

func boardCredential(data []byte, protect bool) ([]byte, error) {
	return nil, errors.New("Activity board credentials require Windows user protection.")
}
