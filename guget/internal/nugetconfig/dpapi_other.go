//go:build !windows

package nugetconfig

import "fmt"

// decryptPassword reports that DPAPI-encrypted passwords are Windows-only.
// Use <ClearTextPassword> in NuGet.Config <packageSourceCredentials> instead.
func decryptPassword(_ string) (string, error) {
	return "", fmt.Errorf("DPAPI password decryption is only supported on Windows; use ClearTextPassword instead")
}
