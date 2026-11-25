//go:build windows

package nugetconfig

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

// nugetEntropy is the optional DPAPI entropy NuGet's EncryptionUtility adds
// when it encrypts a password. Decryption fails without it.
var nugetEntropy = []byte("NuGet")

// decryptPassword decodes a Base64-encoded DPAPI-encrypted NuGet.Config password
// with the current user's key.
func decryptPassword(b64 string) (string, error) {
	encrypted, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", fmt.Errorf("base64 decode: %w", err)
	}

	var inBlob cryptAPIBlob
	if len(encrypted) > 0 {
		inBlob.cbData = uint32(len(encrypted))
		inBlob.pbData = &encrypted[0]
	}

	entropy := cryptAPIBlob{cbData: uint32(len(nugetEntropy)), pbData: &nugetEntropy[0]}
	var outBlob cryptAPIBlob
	r, _, callErr := cryptUnprotectData.Call(
		uintptr(unsafe.Pointer(&inBlob)),
		0,
		uintptr(unsafe.Pointer(&entropy)),
		0, 0, 0,
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
