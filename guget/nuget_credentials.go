package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

var errProviderNotApplicable = errors.New("provider does not handle this source")

type sourceCredential struct {
	Username string
	Password string
}

type credentialProvider struct {
	path  string
	isDLL bool // requires "dotnet exec"
}

type credentialProviderResponse struct {
	Username  string   `json:"Username"`
	Password  string   `json:"Password"`
	AuthTypes []string `json:"AuthTypes"`
}

type pluginMessage struct {
	RequestId string          `json:"RequestId"`
	Type      string          `json:"Type"`
	Method    string          `json:"Method"`
	Payload   json.RawMessage `json:"Payload"`
}

type v2CredentialPayload struct {
	ResponseCode        string   `json:"ResponseCode"`
	Username            string   `json:"Username"`
	Password            string   `json:"Password"`
	Message             string   `json:"Message"`
	AuthenticationTypes []string `json:"AuthenticationTypes"`
}

// normalizeCredentialKey decodes NuGet XML name encoding (e.g. _x0020_ → space).
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

// parseCredentials extracts <packageSourceCredentials> from a NuGet.Config XML blob.
// Element names under packageSourceCredentials are dynamic source names, so we walk tokens manually.
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
				// Element name is the source name.
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
					encPass = value
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
				currentSource = ""
				username = ""
				clearPass = ""
				encPass = ""
			}
		}
	}
	return creds
}

// fetchFromCredentialProvider tries all discovered credential providers in parallel for the given source URL.
func fetchFromCredentialProvider(sourceURL, sourceName string) (*sourceCredential, error) {
	providers := findCredentialProviders()
	if len(providers) == 0 {
		return nil, fmt.Errorf("no credential providers found")
	}

	type providerResult struct {
		cred *sourceCredential
		err  error
		name string
	}

	results := make(chan providerResult, len(providers))
	var wg sync.WaitGroup
	for _, p := range providers {
		wg.Add(1)
		go func(p credentialProvider) {
			defer wg.Done()
			cred, err := invokeProvider(p, sourceURL)
			results <- providerResult{cred, err, filepath.Base(p.path)}
		}(p)
	}
	go func() { wg.Wait(); close(results) }()

	for r := range results {
		if r.err == nil && (r.cred.Username != "" || r.cred.Password != "") {
			logDebug("[%s] credential provider %s supplied credentials", sourceName, r.name)
			return r.cred, nil
		}
		logDebug("[%s] provider %s: %v", sourceName, r.name, r.err)
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
	seen := make(map[string]bool)

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

	if envPaths := os.Getenv("NUGET_CREDENTIALPROVIDER_PLUGIN_PATHS"); envPaths != "" {
		logTrace("findCredentialProviders: NUGET_CREDENTIALPROVIDER_PLUGIN_PATHS=%q", envPaths)
		for _, dir := range strings.Split(envPaths, string(os.PathListSeparator)) {
			for _, p := range findProvidersInDir(dir) {
				add(p)
			}
		}
	}

	if home, err := os.UserHomeDir(); err == nil {
		netcoreDir := filepath.Join(home, ".nuget", "plugins", "netcore")
		logTrace("findCredentialProviders: scanning %q", netcoreDir)
		for _, p := range findProvidersInDir(netcoreDir) {
			add(p)
		}

		if runtime.GOOS == "windows" {
			netfxDir := filepath.Join(home, ".nuget", "plugins", "netfx")
			logTrace("findCredentialProviders: scanning %q", netfxDir)
			for _, p := range findProvidersInDir(netfxDir) {
				add(p)
			}
		}
	}

	if runtime.GOOS == "windows" {
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			v1Dir := filepath.Join(localAppData, "NuGet", "CredentialProviders")
			logTrace("findCredentialProviders: scanning V1 dir %q", v1Dir)
			for _, p := range findV1ProvidersInDir(v1Dir) {
				add(p)
			}
		}
	}

	for _, p := range findPluginsOnPath() {
		add(p)
	}

	logTrace("findCredentialProviders: found %d provider(s)", len(providers))
	return providers
}

// findProvidersInDir scans dir for sub-directories that contain a credential
// provider executable or DLL whose name matches the directory name.
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

// findV1ProvidersInDir scans for credentialprovider*.exe at root level and in subdirectories.
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
			if strings.HasPrefix(nameLower, "credentialprovider") && strings.HasSuffix(nameLower, ".exe") {
				p := filepath.Join(dir, entry.Name())
				logTrace("findV1ProvidersInDir: found %q", p)
				providers = append(providers, credentialProvider{path: p, isDLL: false})
			}
			continue
		}
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

// findPluginsOnPath scans PATH for executables matching nuget-plugin-*.
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
				if strings.HasSuffix(nameLower, ".exe") || strings.HasSuffix(nameLower, ".bat") {
					logTrace("findPluginsOnPath: found %q", fullPath)
					providers = append(providers, credentialProvider{path: fullPath, isDLL: false})
				}
			} else {
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

// invokeProvider tries V2 first, falling back to V1 if the provider doesn't speak V2.
func invokeProvider(provider credentialProvider, sourceURL string) (*sourceCredential, error) {
	name := filepath.Base(provider.path)

	cred, err := invokeProviderV2(provider, sourceURL)
	if err == nil && (cred.Username != "" || cred.Password != "") {
		return cred, nil
	}
	if errors.Is(err, errProviderNotApplicable) {
		return nil, err
	}

	logDebug("[%s] V2 returned no credentials, trying V1 protocol", name)
	return invokeProviderV1(provider, sourceURL)
}

// invokeProviderV1 calls a credential provider using the V1 command-line args protocol.
func invokeProviderV1(provider credentialProvider, sourceURL string) (*sourceCredential, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	args := []string{"-Uri", sourceURL, "-NonInteractive", "-IsRetry", "false"}
	if provider.isDLL {
		dotnetArgs := append([]string{"exec", provider.path}, args...)
		cmd = exec.CommandContext(ctx, "dotnet", dotnetArgs...)
		logTrace("invokeProviderV1: running dotnet exec %s", filepath.Base(provider.path))
	} else {
		cmd = exec.CommandContext(ctx, provider.path, args...)
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("provider exited non-zero: %w", err)
	}
	logTrace("invokeProviderV1: %s produced %d bytes of output", filepath.Base(provider.path), len(out))

	// Credential providers sometimes emit informational lines to stdout before
	// the JSON payload (e.g. "INFO: ..."). Find the first '{' to locate the JSON.
	jsonStart := bytes.IndexByte(out, '{')
	if jsonStart >= 0 {
		logTrace("invokeProviderV1: JSON found at offset %d (preamble: %d bytes)", jsonStart, jsonStart)
		var resp credentialProviderResponse
		if err := json.Unmarshal(out[jsonStart:], &resp); err != nil {
			return nil, fmt.Errorf("parsing provider output: %w", err)
		}
		logTrace("invokeProviderV1: JSON parsed OK (username=%q, password=%d chars)", resp.Username, len(resp.Password))
		return &sourceCredential{Username: resp.Username, Password: resp.Password}, nil
	}

	// Fallback: some providers emit credentials as log lines instead of JSON, e.g.:
	//   [Information] [CredentialProvider]Username: VssSessionToken
	//   [Information] [CredentialProvider]Password: abc123
	logTrace("invokeProviderV1: no JSON found, trying log-line parse")
	cred := parseLogLineCredentials(out)
	if cred != nil {
		logTrace("invokeProviderV1: log-line parse OK (username=%q, password=%d chars)", cred.Username, len(cred.Password))
		return cred, nil
	}

	return nil, fmt.Errorf("no credentials in V1 output")
}

// invokeProviderV2 calls a credential provider using the V2 stdin/stdout JSON protocol.
func invokeProviderV2(provider credentialProvider, sourceURL string) (*sourceCredential, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// CredentialProvider.Microsoft requires -Plugin to enter V2 mode.
	var args []string
	if strings.Contains(strings.ToLower(filepath.Base(provider.path)), "credentialprovider.microsoft") {
		args = []string{"-Plugin"}
	}

	var cmd *exec.Cmd
	if provider.isDLL {
		dotnetArgs := append([]string{"exec", provider.path}, args...)
		cmd = exec.CommandContext(ctx, "dotnet", dotnetArgs...)
	} else {
		cmd = exec.CommandContext(ctx, provider.path, args...)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("creating stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("creating stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting provider: %w", err)
	}
	defer func() {
		stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	scanner := bufio.NewScanner(stdout)

	// 1. Read Handshake Request from plugin.
	if !scanner.Scan() {
		return nil, fmt.Errorf("V2: no handshake from plugin")
	}
	var handshake pluginMessage
	if err := json.Unmarshal(scanner.Bytes(), &handshake); err != nil {
		return nil, fmt.Errorf("V2: parsing handshake: %w", err)
	}
	if handshake.Method != "Handshake" {
		return nil, fmt.Errorf("V2: expected Handshake, got %q", handshake.Method)
	}
	logTrace("invokeProviderV2: received Handshake (RequestId=%s)", handshake.RequestId)

	// Handshake succeeded — provider speaks V2, so all errors below
	// wrap errProviderNotApplicable to skip V1 fallback.

	// 2. Send Handshake Response.
	writePluginMessage(stdin, pluginMessage{
		RequestId: handshake.RequestId,
		Type:      "Response",
		Method:    "Handshake",
		Payload:   json.RawMessage(`{"ResponseCode":"Success","ProtocolVersion":"2.0.0"}`),
	})
	logTrace("invokeProviderV2: sent Handshake response")

	// 3. Send GetAuthenticationCredentials Request.
	credReqId := newRequestID()
	payloadJSON, _ := json.Marshal(map[string]any{
		"Uri":              sourceURL,
		"IsRetry":          false,
		"IsNonInteractive": true,
		"CanShowDialog":    false,
	})
	writePluginMessage(stdin, pluginMessage{
		RequestId: credReqId,
		Type:      "Request",
		Method:    "GetAuthenticationCredentials",
		Payload:   json.RawMessage(payloadJSON),
	})
	logTrace("invokeProviderV2: sent GetAuthenticationCredentials (RequestId=%s, Uri=%s)", credReqId, sourceURL)

	// 4. Read messages until we get the credential response.
	for scanner.Scan() {
		var msg pluginMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			logTrace("invokeProviderV2: skipping unparseable line: %s", scanner.Text())
			continue
		}
		logTrace("invokeProviderV2: received %s/%s (RequestId=%s)", msg.Type, msg.Method, msg.RequestId)

		if msg.RequestId == credReqId && msg.Type == "Response" {
			var creds v2CredentialPayload
			if err := json.Unmarshal(msg.Payload, &creds); err != nil {
				return nil, fmt.Errorf("V2: parsing credential payload: %w: %w", err, errProviderNotApplicable)
			}
			if creds.ResponseCode == "NotFound" {
				logTrace("invokeProviderV2: provider does not handle this source")
				return nil, errProviderNotApplicable
			}
			if creds.ResponseCode != "Success" {
				return nil, fmt.Errorf("V2: provider returned %s: %s: %w", creds.ResponseCode, creds.Message, errProviderNotApplicable)
			}
			logTrace("invokeProviderV2: credentials received (username=%q, password=%d chars)", creds.Username, len(creds.Password))
			return &sourceCredential{Username: creds.Username, Password: creds.Password}, nil
		}
	}

	return nil, fmt.Errorf("V2: plugin closed stdout without credential response: %w", errProviderNotApplicable)
}

// writePluginMessage writes a JSON-encoded plugin message to w.
func writePluginMessage(w interface{ Write([]byte) (int, error) }, msg pluginMessage) {
	data, _ := json.Marshal(msg)
	data = append(data, '\n')
	_, _ = w.Write(data)
}

// newRequestID generates a random UUID v4 string.
func newRequestID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// parseLogLineCredentials handles providers that write credentials as log lines rather than JSON.
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
