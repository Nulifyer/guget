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

// credentialProvider represents a discovered NuGet credential provider plugin.
type credentialProvider struct {
	path  string // full path to the executable or DLL
	isDLL bool   // true if the provider is a .dll requiring "dotnet exec"
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

// fetchFromCredentialProvider tries all discovered credential providers for the given source URL.
func fetchFromCredentialProvider(sourceURL, sourceName string) (*sourceCredential, error) {
	providers := findCredentialProviders()
	if len(providers) == 0 {
		return nil, fmt.Errorf("no credential providers found")
	}
	for _, p := range providers {
		cred, err := invokeProvider(p, sourceURL)
		if err == nil && (cred.Username != "" || cred.Password != "") {
			logDebug("[%s] credential provider %s supplied credentials", sourceName, filepath.Base(p.path))
			return cred, nil
		}
		logDebug("[%s] provider %s: %v", sourceName, filepath.Base(p.path), err)
	}
	return nil, fmt.Errorf("no credential provider succeeded for %q", sourceName)
}

// findCredentialProviders discovers NuGet credential provider plugins using the
// full NuGet specification search order:
//  1. NUGET_NETCORE_PLUGIN_PATHS — full paths to .NET Core plugins (highest priority)
//  2. NUGET_PLUGIN_PATHS          — full paths to plugins
//  3. NUGET_CREDENTIALPROVIDER_PLUGIN_PATHS — directories to scan (legacy)
//  4. ~/.nuget/plugins/netcore/   — cross-platform convention directory
//  5. ~/.nuget/plugins/netfx/     — Windows .NET Framework convention directory
//  6. %LocalAppData%\NuGet\CredentialProviders — legacy V1 provider directory (Windows)
//  7. PATH scan for nuget-plugin-* — .NET tool-installed providers
func findCredentialProviders() []credentialProvider {
	var providers []credentialProvider
	seen := make(map[string]bool) // deduplicate by absolute path

	add := func(p credentialProvider) {
		abs, err := filepath.Abs(p.path)
		if err != nil {
			abs = p.path
		}
		key := strings.ToLower(abs)
		if seen[key] {
			logTrace("findCredentialProviders: skipping duplicate %q", p.path)
			return
		}
		seen[key] = true
		providers = append(providers, p)
	}

	// 1. NUGET_NETCORE_PLUGIN_PATHS — full paths to plugin executables/DLLs
	if envPaths := os.Getenv("NUGET_NETCORE_PLUGIN_PATHS"); envPaths != "" {
		logTrace("findCredentialProviders: NUGET_NETCORE_PLUGIN_PATHS=%q", envPaths)
		for _, p := range strings.Split(envPaths, string(os.PathListSeparator)) {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if _, err := os.Stat(p); err == nil {
				add(credentialProvider{path: p, isDLL: strings.HasSuffix(strings.ToLower(p), ".dll")})
			}
		}
	}

	// 2. NUGET_PLUGIN_PATHS — full paths to plugin executables/DLLs
	if envPaths := os.Getenv("NUGET_PLUGIN_PATHS"); envPaths != "" {
		logTrace("findCredentialProviders: NUGET_PLUGIN_PATHS=%q", envPaths)
		for _, p := range strings.Split(envPaths, string(os.PathListSeparator)) {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if _, err := os.Stat(p); err == nil {
				add(credentialProvider{path: p, isDLL: strings.HasSuffix(strings.ToLower(p), ".dll")})
			}
		}
	}

	// 3. NUGET_CREDENTIALPROVIDER_PLUGIN_PATHS — directories to scan (legacy)
	if envPaths := os.Getenv("NUGET_CREDENTIALPROVIDER_PLUGIN_PATHS"); envPaths != "" {
		logTrace("findCredentialProviders: NUGET_CREDENTIALPROVIDER_PLUGIN_PATHS=%q", envPaths)
		for _, dir := range strings.Split(envPaths, string(os.PathListSeparator)) {
			for _, p := range findProvidersInDir(dir) {
				add(p)
			}
		}
	}

	if home, err := os.UserHomeDir(); err == nil {
		// 4. ~/.nuget/plugins/netcore/ (cross-platform)
		netcoreDir := filepath.Join(home, ".nuget", "plugins", "netcore")
		logTrace("findCredentialProviders: scanning %q", netcoreDir)
		for _, p := range findProvidersInDir(netcoreDir) {
			add(p)
		}

		// 5. ~/.nuget/plugins/netfx/ (Windows only)
		if runtime.GOOS == "windows" {
			netfxDir := filepath.Join(home, ".nuget", "plugins", "netfx")
			logTrace("findCredentialProviders: scanning %q", netfxDir)
			for _, p := range findProvidersInDir(netfxDir) {
				add(p)
			}
		}
	}

	// 6. %LocalAppData%\NuGet\CredentialProviders (Windows V1 providers)
	if runtime.GOOS == "windows" {
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			v1Dir := filepath.Join(localAppData, "NuGet", "CredentialProviders")
			logTrace("findCredentialProviders: scanning V1 dir %q", v1Dir)
			for _, p := range findV1ProvidersInDir(v1Dir) {
				add(p)
			}
		}
	}

	// 7. PATH scan for nuget-plugin-* (.NET tool-installed providers)
	for _, p := range findPluginsOnPath() {
		add(p)
	}

	logTrace("findCredentialProviders: found %d provider(s)", len(providers))
	return providers
}

// findProvidersInDir scans dir for sub-directories that contain a credential
// provider executable or DLL whose name matches the directory name.
// This handles both the standard "CredentialProvider.*" convention and
// non-standard names like "AWS.CodeArtifact.NuGetCredentialProvider".
func findProvidersInDir(dir string) []credentialProvider {
	entries, err := os.ReadDir(dir)
	if err != nil {
		logTrace("findProvidersInDir: cannot read %q: %v", dir, err)
		return nil
	}
	var providers []credentialProvider
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		subDir := filepath.Join(dir, entry.Name())
		name := entry.Name()

		// Prefer native executable first, then fall back to DLL.
		if runtime.GOOS == "windows" {
			exePath := filepath.Join(subDir, name+".exe")
			if _, err := os.Stat(exePath); err == nil {
				logTrace("findProvidersInDir: found exe provider %q", exePath)
				providers = append(providers, credentialProvider{path: exePath, isDLL: false})
				continue
			}
		} else {
			exePath := filepath.Join(subDir, name)
			if _, err := os.Stat(exePath); err == nil {
				logTrace("findProvidersInDir: found provider %q", exePath)
				providers = append(providers, credentialProvider{path: exePath, isDLL: false})
				continue
			}
		}

		// Fall back to DLL (requires dotnet exec).
		dllPath := filepath.Join(subDir, name+".dll")
		if _, err := os.Stat(dllPath); err == nil {
			logTrace("findProvidersInDir: found DLL provider %q", dllPath)
			providers = append(providers, credentialProvider{path: dllPath, isDLL: true})
			continue
		}

		logTrace("findProvidersInDir: no executable or DLL found in %q", subDir)
	}
	return providers
}

// findV1ProvidersInDir scans a directory for legacy V1 NuGet credential providers
// (files matching credentialprovider*.exe at the root or in subdirectories).
func findV1ProvidersInDir(dir string) []credentialProvider {
	entries, err := os.ReadDir(dir)
	if err != nil {
		logTrace("findV1ProvidersInDir: cannot read %q: %v", dir, err)
		return nil
	}
	var providers []credentialProvider
	for _, entry := range entries {
		nameLower := strings.ToLower(entry.Name())
		if !entry.IsDir() {
			// Root-level credentialprovider*.exe
			if strings.HasPrefix(nameLower, "credentialprovider") && strings.HasSuffix(nameLower, ".exe") {
				p := filepath.Join(dir, entry.Name())
				logTrace("findV1ProvidersInDir: found %q", p)
				providers = append(providers, credentialProvider{path: p, isDLL: false})
			}
			continue
		}
		// Scan subdirectory for credentialprovider*.exe
		subEntries, err := os.ReadDir(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		for _, sub := range subEntries {
			subLower := strings.ToLower(sub.Name())
			if !sub.IsDir() && strings.HasPrefix(subLower, "credentialprovider") && strings.HasSuffix(subLower, ".exe") {
				p := filepath.Join(dir, entry.Name(), sub.Name())
				logTrace("findV1ProvidersInDir: found %q", p)
				providers = append(providers, credentialProvider{path: p, isDLL: false})
			}
		}
	}
	return providers
}

// findPluginsOnPath scans all directories in PATH for executables whose name
// starts with "nuget-plugin-" (.NET tool-installed credential providers).
func findPluginsOnPath() []credentialProvider {
	pathEnv := os.Getenv("PATH")
	if pathEnv == "" {
		return nil
	}
	var providers []credentialProvider
	for _, dir := range strings.Split(pathEnv, string(os.PathListSeparator)) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			nameLower := strings.ToLower(name)
			if !strings.HasPrefix(nameLower, "nuget-plugin-") {
				continue
			}
			fullPath := filepath.Join(dir, name)
			if runtime.GOOS == "windows" {
				// On Windows accept .exe and .bat
				if strings.HasSuffix(nameLower, ".exe") || strings.HasSuffix(nameLower, ".bat") {
					logTrace("findPluginsOnPath: found %q", fullPath)
					providers = append(providers, credentialProvider{path: fullPath, isDLL: false})
				}
			} else {
				// On Unix, check the file is executable
				if info, err := entry.Info(); err == nil && info.Mode()&0111 != 0 {
					logTrace("findPluginsOnPath: found %q", fullPath)
					isDLL := strings.HasSuffix(nameLower, ".dll")
					providers = append(providers, credentialProvider{path: fullPath, isDLL: isDLL})
				}
			}
		}
	}
	return providers
}

// invokeProvider calls a credential provider executable using the NuGet plugin protocol
// and parses the JSON response. DLL-based providers are invoked via "dotnet exec".
func invokeProvider(provider credentialProvider, sourceURL string) (*sourceCredential, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	args := []string{"-Uri", sourceURL, "-NonInteractive", "-IsRetry", "false"}
	if provider.isDLL {
		dotnetArgs := append([]string{"exec", provider.path}, args...)
		cmd = exec.CommandContext(ctx, "dotnet", dotnetArgs...)
		logTrace("invokeProvider: running dotnet exec %s", filepath.Base(provider.path))
	} else {
		cmd = exec.CommandContext(ctx, provider.path, args...)
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("provider exited non-zero: %w", err)
	}
	logTrace("invokeProvider: %s produced %d bytes of output", filepath.Base(provider.path), len(out))

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
