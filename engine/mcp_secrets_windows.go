//go:build windows

package main

import (
	"errors"
	"golang.org/x/sys/windows"
	"unsafe"
)

func protectMCPCredential(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("Empty credential.")
	}
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, errors.New("Cannot protect MCP credentials for the current Windows user.")
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte{}, unsafe.Slice(out.Data, int(out.Size))...), nil
}

func unprotectMCPCredential(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("Empty credential.")
	}
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte{}, unsafe.Slice(out.Data, int(out.Size))...), nil
}
