//go:build !windows

package main

import "fmt"

// decryptNuGetPassword is not supported on non-Windows platforms.
// Use <ClearTextPassword> in NuGet.Config <packageSourceCredentials> instead.
func decryptNuGetPassword(_ string) (string, error) {
	return "", fmt.Errorf("DPAPI password decryption is only supported on Windows; use ClearTextPassword instead")
}
