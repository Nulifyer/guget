package packageops

import "testing"

func TestDecodeCommandJSONAllowsDiagnosticPrefix(t *testing.T) {
	var value struct {
		Version int `json:"version"`
	}
	if err := decodeCommandJSON([]byte("warning: stale assets\n{\"version\":1}\n"), &value); err != nil {
		t.Fatal(err)
	}
	if value.Version != 1 {
		t.Fatalf("version = %d", value.Version)
	}
}
