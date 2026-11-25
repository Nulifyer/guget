//go:build windows

package nugetconfig

import (
	"os/exec"
	"strings"
	"testing"
)

// TestDecryptPasswordReadsNuGetEncryption encrypts a password the way NuGet's
// EncryptionUtility does: DPAPI for the current user over the UTF-8 bytes,
// with the entropy "NuGet". PowerShell's ProtectedData is the same .NET API.
func TestDecryptPasswordReadsNuGetEncryption(t *testing.T) {
	const script = `Add-Type -AssemblyName System.Security
$bytes = [Text.Encoding]::UTF8.GetBytes('p@ss;w' + [char]0xF6 + 'rd')
$entropy = [Text.Encoding]::UTF8.GetBytes('NuGet')
[Convert]::ToBase64String([Security.Cryptography.ProtectedData]::Protect($bytes, $entropy, 'CurrentUser'))`
	output, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).Output()
	if err != nil {
		t.Skipf("PowerShell could not encrypt a password: %v", err)
	}
	got, err := decryptPassword(strings.TrimSpace(string(output)))
	if err != nil {
		t.Fatal(err)
	}
	if want := "p@ss;wörd"; got != want {
		t.Fatalf("decrypted %q, want %q", got, want)
	}
}
