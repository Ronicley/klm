//go:build windows

package main

import (
	"errors"
	"golang.org/x/sys/windows"
	"unsafe"
)

// Separate namespace and DPAPI entropy from Library credentials.
func boardCredential(data []byte, protect bool) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("Board credential is required.")
	}
	entropy := []byte("klm.activityBoard.v1")
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	additional := windows.DataBlob{Size: uint32(len(entropy)), Data: &entropy[0]}
	var out windows.DataBlob
	var err error
	if protect {
		err = windows.CryptProtectData(&in, nil, &additional, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	} else {
		err = windows.CryptUnprotectData(&in, nil, &additional, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	}
	if err != nil {
		return nil, errors.New("Cannot access board credentials for the current Windows user.")
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte{}, unsafe.Slice(out.Data, int(out.Size))...), nil
}
