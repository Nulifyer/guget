//go:build windows

package main

import (
	"encoding/base64"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	crypt32            = windows.NewLazySystemDLL("crypt32.dll")
	cryptUnprotectData = crypt32.NewProc("CryptUnprotectData")
)

type cryptAPIBlob struct {
	cbData uint32
	pbData *byte
}

// decryptNuGetPassword decodes a Base64-encoded DPAPI-encrypted password from NuGet.Config
// and returns the plaintext string. Only available on Windows.
func decryptNuGetPassword(b64 string) (string, error) {
	encrypted, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", fmt.Errorf("base64 decode: %w", err)
	}

	var inBlob cryptAPIBlob
	if len(encrypted) > 0 {
		inBlob.cbData = uint32(len(encrypted))
		inBlob.pbData = &encrypted[0]
	}

	var outBlob cryptAPIBlob
	r, _, callErr := cryptUnprotectData.Call(
		uintptr(unsafe.Pointer(&inBlob)),
		0, 0, 0, 0, 0,
		uintptr(unsafe.Pointer(&outBlob)),
	)
	if r == 0 {
		return "", fmt.Errorf("CryptUnprotectData: %w", callErr)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(outBlob.pbData))) //nolint:errcheck

	plaintext := make([]byte, outBlob.cbData)
	copy(plaintext, unsafe.Slice(outBlob.pbData, outBlob.cbData))
	return string(plaintext), nil
}
