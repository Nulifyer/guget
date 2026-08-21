package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestLoggingRedactsCredentialsAndSensitiveQueryValues(t *testing.T) {
	var output bytes.Buffer
	oldLevel, oldOut, oldErr := logLevel, logOutWriter, logErrWriter
	t.Cleanup(func() {
		logLevel, logOutWriter, logErrWriter = oldLevel, oldOut, oldErr
	})
	logSetOutput(&output)
	logSetLevel(LogLevelTrace)
	logSetColor(false)
	logRegisterSecret("super-secret-value")

	logError("request https://user:pass@example.test/index.json?token=abc123 failed: %s", "super-secret-value")
	got := output.String()
	for _, secret := range []string{"user:pass", "abc123", "super-secret-value"} {
		if strings.Contains(got, secret) {
			t.Fatalf("log leaked %q: %s", secret, got)
		}
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("log did not show redaction marker: %s", got)
	}
}
