package packageops

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNuGetGraphInspectorUsesSDKVersionAwareCommand(t *testing.T) {
	var packageArgs []string
	runner := fakeRunner(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) == 1 && args[0] == "--version" {
			return []byte("10.0.111\n"), nil
		}
		packageArgs = append([]string(nil), args...)
		return []byte(`{"version":1,"projects":[{"path":"App.csproj","frameworks":[{"framework":"net8.0","topLevelPackages":[{"id":"Example.Core","requestedVersion":"[1.0,2.0)","resolvedVersion":"1.5.0"}]}]}]}`), nil
	})
	uses, err := (NuGetGraphInspector{Runner: runner}).ResolveProject(context.Background(), "App.csproj", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(packageArgs) < 2 || packageArgs[0] != "package" || packageArgs[1] != "list" {
		t.Fatalf("args = %v", packageArgs)
	}
	if len(uses) != 1 || uses[0].EvaluatedExpression != "[1.0,2.0)" || uses[0].ResolvedVersion != "1.5.0" {
		t.Fatalf("uses = %#v", uses)
	}
}

func TestNuGetGraphInspectorUsesLegacyOrderingBeforeDotnet10(t *testing.T) {
	var packageArgs []string
	runner := fakeRunner(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) == 1 && args[0] == "--version" {
			return []byte("9.0.300"), nil
		}
		packageArgs = append([]string(nil), args...)
		return []byte(`{"version":1,"projects":[]}`), nil
	})
	_, err := (NuGetGraphInspector{Runner: runner}).ResolveProject(context.Background(), "App.csproj", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(packageArgs[:3], " "); got != "list App.csproj package" {
		t.Fatalf("args = %v", packageArgs)
	}
}

func TestNuGetGraphInspectorReportsWhyTheGraphIsUnavailable(t *testing.T) {
	const missing = "No assets file was found for `App.csproj`. Please run restore before running this command."
	for _, test := range []struct {
		name, output string
		notRestored  bool
		want         string
	}{
		{"problems in the JSON output", `{"version":1,"problems":[{"project":"App.csproj","level":"error","text":"` + strings.ReplaceAll(missing, "`", "\\u0060") + `"}],"projects":[{"path":"App.csproj"}]}`, true, missing},
		{"no output", "", false, "exit status 1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := fakeRunner(func(_ context.Context, _ string, args ...string) ([]byte, error) {
				if len(args) == 1 && args[0] == "--version" {
					return []byte("10.0.111"), nil
				}
				return []byte(test.output), errors.New("exit status 1")
			})
			_, err := (NuGetGraphInspector{Runner: runner}).ResolveProject(context.Background(), "App.csproj", false)
			var notRestored *NotRestoredError
			if err == nil || errors.As(err, &notRestored) != test.notRestored || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
