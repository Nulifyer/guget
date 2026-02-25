package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

)

// sourceCredential holds decoded credentials for a NuGet source.
type sourceCredential struct {
	Username string
	Password string
}

// credentialProviderResponse is the JSON payload returned by NuGet credential providers.
type credentialProviderResponse struct {
	Username  string   `json:"Username"`
	Password  string   `json:"Password"`
	AuthTypes []string `json:"AuthTypes"`
}

// normalizeCredentialKey decodes NuGet XML name encoding (e.g. _x0020_ → space)
// and returns a lowercase string suitable for credential-to-source matching.
func normalizeCredentialKey(name string) string {
	var result strings.Builder
	i := 0
	for i < len(name) {
		// NuGet uses XmlConvert.EncodeName: spaces → _x0020_, etc.
		if name[i] == '_' && i+2 < len(name) && name[i+1] == 'x' {
			j := strings.IndexByte(name[i+2:], '_')
			if j >= 0 {
				hex := name[i+2 : i+2+j]
				if r, err := strconv.ParseInt(hex, 16, 32); err == nil {
					result.WriteRune(rune(r))
					i = i + 2 + j + 1
					continue
				}
			}
		}
		result.WriteByte(name[i])
		i++
	}
	return strings.ToLower(result.String())
}

// parseCredentials extracts <packageSourceCredentials> entries from a NuGet.Config XML blob.
// The element name under packageSourceCredentials is the source name (dynamic), so we walk
// tokens manually rather than relying on static struct unmarshalling.
// Returns a map of normalised source name → credentials.
func parseCredentials(data []byte) map[string]sourceCredential {
	creds := make(map[string]sourceCredential)
	dec := xml.NewDecoder(bytes.NewReader(data))
	logTrace("parseCredentials: parsing %d bytes", len(data))

	inSection := false
	var currentSource string
	var username, clearPass, encPass string

	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch {
			case t.Name.Local == "packageSourceCredentials":
				inSection = true
			case inSection && currentSource == "":
				// The element name IS the source name.
				currentSource = t.Name.Local
				logTrace("parseCredentials: found credential block for source %q", currentSource)
			case inSection && currentSource != "" && t.Name.Local == "add":
				var key, value string
				for _, attr := range t.Attr {
					switch attr.Name.Local {
					case "key":
						key = attr.Value
					case "value":
						value = attr.Value
					}
				}
				switch strings.ToLower(key) {
				case "username":
					username = value
					logTrace("parseCredentials: [%s] username = %q", currentSource, username)
				case "cleartextpassword":
					clearPass = value
					logTrace("parseCredentials: [%s] ClearTextPassword present (%d chars)", currentSource, len(clearPass))
				case "password":
					encPass = value // DPAPI-encrypted (Windows)
					logTrace("parseCredentials: [%s] encrypted Password present (%d chars)", currentSource, len(encPass))
				}
			}

		case xml.EndElement:
			switch {
			case t.Name.Local == "packageSourceCredentials":
				inSection = false
			case inSection && t.Name.Local == currentSource && currentSource != "":
				password := clearPass
				if password == "" && encPass != "" {
					if p, err := decryptNuGetPassword(encPass); err == nil {
						password = p
					} else {
						logDebug("DPAPI decryption failed for source %q: %v", currentSource, err)
					}
				}
				if username != "" || password != "" {
					if username == "" && password != "" {
						username = "PAT"
						logTrace("parseCredentials: [%s] no username set, defaulting to %q", currentSource, username)
					}
					key := normalizeCredentialKey(currentSource)
					logTrace("parseCredentials: [%s] stored credential under key %q (username=%q, password=%d chars)", currentSource, key, username, len(password))
					creds[key] = sourceCredential{Username: username, Password: password}
				} else {
					logTrace("parseCredentials: [%s] no credentials found in block", currentSource)
				}
				// Reset state for next source element
				currentSource = ""
				username = ""
				clearPass = ""
				encPass = ""
			}
		}
	}
	return creds
}

// ─────────────────────────────────────────────
// NuGet credential provider protocol
// ─────────────────────────────────────────────

// fetchFromCredentialProvider tries all discovered credential providers for the given source URL.
func fetchFromCredentialProvider(sourceURL, sourceName string) (*sourceCredential, error) {
	providers := findCredentialProviders()
	if len(providers) == 0 {
		return nil, fmt.Errorf("no credential providers found")
	}
	for _, p := range providers {
		cred, err := invokeProvider(p, sourceURL)
		if err == nil && (cred.Username != "" || cred.Password != "") {
			logDebug("[%s] credential provider %s supplied credentials", sourceName, filepath.Base(p))
			return cred, nil
		}
		logDebug("[%s] provider %s: %v", sourceName, filepath.Base(p), err)
	}
	return nil, fmt.Errorf("no credential provider succeeded for %q", sourceName)
}

// findCredentialProviders returns paths to NuGet credential provider executables.
// It checks NUGET_CREDENTIALPROVIDER_PLUGIN_PATHS first, then the standard
// ~/.nuget/plugins/netcore/ directory (which is where the Azure Artifacts
// Credential Provider installs itself on all platforms).
func findCredentialProviders() []string {
	var providers []string

	// 1. Explicit env var (semicolon on Windows, colon on Unix)
	if envPaths := os.Getenv("NUGET_CREDENTIALPROVIDER_PLUGIN_PATHS"); envPaths != "" {
		logTrace("findCredentialProviders: NUGET_CREDENTIALPROVIDER_PLUGIN_PATHS=%q", envPaths)
		for _, dir := range strings.Split(envPaths, string(os.PathListSeparator)) {
			providers = append(providers, findProvidersInDir(dir)...)
		}
	} else {
		logTrace("findCredentialProviders: NUGET_CREDENTIALPROVIDER_PLUGIN_PATHS not set")
	}

	// 2. Standard per-user plugin directory
	if home, err := os.UserHomeDir(); err == nil {
		dir := filepath.Join(home, ".nuget", "plugins", "netcore")
		logTrace("findCredentialProviders: scanning standard dir %q", dir)
		providers = append(providers, findProvidersInDir(dir)...)
	}

	logTrace("findCredentialProviders: found %d provider(s)", len(providers))
	return providers
}

// findProvidersInDir scans dir for CredentialProvider.* sub-directories and returns
// the path to the matching executable inside each one.
func findProvidersInDir(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		logTrace("findProvidersInDir: cannot read %q: %v", dir, err)
		return nil
	}
	var providers []string
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "CredentialProvider.") {
			continue
		}
		exeName := entry.Name()
		if runtime.GOOS == "windows" {
			exeName += ".exe"
		}
		exePath := filepath.Join(dir, entry.Name(), exeName)
		if _, err := os.Stat(exePath); err == nil {
			logTrace("findProvidersInDir: found provider %q", exePath)
			providers = append(providers, exePath)
		} else {
			logTrace("findProvidersInDir: expected exe not found at %q: %v", exePath, err)
		}
	}
	return providers
}

// invokeProvider calls a credential provider executable using the NuGet plugin protocol
// and parses the JSON response.
func invokeProvider(providerPath, sourceURL string) (*sourceCredential, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, providerPath,
		"-Uri", sourceURL,
		"-NonInteractive",
		"-IsRetry", "false",
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("provider exited non-zero: %w", err)
	}
	logTrace("invokeProvider: %s produced %d bytes of output", filepath.Base(providerPath), len(out))

	// Credential providers sometimes emit informational lines to stdout before
	// the JSON payload (e.g. "INFO: ..."). Find the first '{' to locate the JSON.
	jsonStart := bytes.IndexByte(out, '{')
	if jsonStart >= 0 {
		logTrace("invokeProvider: JSON found at offset %d (preamble: %d bytes)", jsonStart, jsonStart)
		var resp credentialProviderResponse
		if err := json.Unmarshal(out[jsonStart:], &resp); err != nil {
			return nil, fmt.Errorf("parsing provider output: %w", err)
		}
		logTrace("invokeProvider: JSON parsed OK (username=%q, password=%d chars)", resp.Username, len(resp.Password))
		return &sourceCredential{Username: resp.Username, Password: resp.Password}, nil
	}

	// Fallback: some providers emit credentials as log lines instead of JSON, e.g.:
	//   [Information] [CredentialProvider]Username: VssSessionToken
	//   [Information] [CredentialProvider]Password: abc123
	logTrace("invokeProvider: no JSON found, trying log-line parse")
	cred := parseLogLineCredentials(out)
	if cred != nil {
		logTrace("invokeProvider: log-line parse OK (username=%q, password=%d chars)", cred.Username, len(cred.Password))
		return cred, nil
	}

	logTrace("invokeProvider: raw output: %q", string(out))
	return nil, fmt.Errorf("parsing provider output: no JSON object or recognisable credential lines found in output")
}

// parseLogLineCredentials handles providers that write credentials as log lines rather
// than JSON, e.g.:
//
//	[Information] [CredentialProvider]Username: VssSessionToken
//	[Information] [CredentialProvider]Password: abc123
//
// Returns nil if no credential lines are found.
func parseLogLineCredentials(out []byte) *sourceCredential {
	var username, password string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if i := strings.Index(line, "]Username: "); i >= 0 {
			username = strings.TrimSpace(line[i+len("]Username: "):])
		} else if i := strings.Index(line, "]Password: "); i >= 0 {
			password = strings.TrimSpace(line[i+len("]Password: "):])
		}
	}
	if username == "" && password == "" {
		return nil
	}
	return &sourceCredential{Username: username, Password: password}
}
