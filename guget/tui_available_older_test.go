package main

import (
	"strings"
	"testing"

	text "github.com/nulifyer/guget/internal/ui/text"
)

// The Available column never names a version older than the one in use: a
// reference on a preview is not shown the older stable as a target.
func TestAvailableColumnNeverShowsAnOlderVersion(t *testing.T) {
	initTheme("auto-dark", false)
	version := func(v string) *PackageVersion {
		if v == "" {
			return nil
		}
		return &PackageVersion{SemVer: ParseSemVer(v)}
	}
	tests := []struct {
		name                    string
		inUse, oldest           string
		compatible, stable, out string
	}{
		{"both older than a preview", "11.0.0-preview.6", "", "10.7.0", "10.7.0", "-"},
		{"older stable beside a newer preview", "11.0.0-preview.6", "", "11.0.0-preview.7", "10.7.0", "11.0.0-preview.7"},
		{"older target beside a newer stable", "2.0.0", "", "1.0.0", "3.0.0", "- (3.0.0)"},
		{"older stable and no target", "2.0.0", "", "", "1.0.0", "-"},
		{"target is the version in use", "2.0.0", "", "2.0.0", "2.0.0", "2.0.0"},
		{"stable is the version in use", "11.0.0", "", "11.1.0-preview.1", "11.0.0", "11.1.0-preview.1 (11.0.0)"},
		{"newer versions", "1.0.0", "", "2.0.0", "3.0.0", "2.0.0 (3.0.0)"},
		// Projects differ: 2.0.0 is still an update for the one on 1.0.0.
		{"between the versions of two projects", "3.0.0", "1.0.0", "2.0.0", "2.0.0", "2.0.0"},
		{"older than every project", "3.0.0", "2.0.0", "1.0.0", "1.0.0", "-"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row := packageRow{
				ref:              PackageReference{Name: "A", Version: ParseSemVer(tt.inUse)},
				latestCompatible: version(tt.compatible),
				latestStable:     version(tt.stable),
			}
			if tt.oldest != "" {
				row.diverged, row.oldest = true, ParseSemVer(tt.oldest)
			}
			if got := availableVersionText(row); got != tt.out {
				t.Fatalf("text = %q, want %q", got, tt.out)
			}
			if got := text.Strip(renderAvailableVersion(row)); got != tt.out {
				t.Fatalf("styled = %q, want %q", got, tt.out)
			}
			// The narrow renderer builds on the same parts.
			if got := text.Strip(renderAvailableCell(row, len(tt.out)+1)); got != tt.out {
				t.Fatalf("cell = %q, want %q", got, tt.out)
			}
		})
	}
}

// A reference on a preview that is newer than every listed stable version:
// neither the list nor the detail panel presents the older stable as an update.
func TestPreviewReferenceIsNotOfferedAnOlderStable(t *testing.T) {
	initTheme("auto-dark", false)
	for _, policy := range []UpdateTarget{UpdateTargetCompatibleStable, UpdateTargetStable, UpdateTargetCompatible} {
		t.Run(string(policy), func(t *testing.T) {
			m, _, _ := newActionTestApp(t, `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="A" Version="11.0.0-preview.6"/></ItemGroup></Project>`)
			m.updateTarget = policy
			setTestPackage(m, "A", "10.6.0", "10.7.0", "11.0.0-preview.6")
			row := selectedRow(m)
			if row.updateTarget != nil || row.statusIcon() != "✓" {
				t.Fatalf("target %s icon %s", versionText(row.updateTarget), row.statusIcon())
			}
			if got := availableVersionText(row); strings.Contains(got, "10.") {
				t.Fatalf("Available = %q", got)
			}
			if len(row.updateRequirements) != 0 {
				t.Fatalf("requirements of a version that is not an update: %v", row.updateRequirements)
			}
			detail := text.Strip(m.renderDetail(row))
			if strings.Contains(detail, "↑ ") || strings.Contains(detail, "⇑ ") || strings.Contains(detail, "Newer version not offered") {
				t.Fatalf("detail marks an older version as an update:\n%s", detail)
			}
			// The version list itself stays complete.
			if !strings.Contains(detail, "10.7.0") || !strings.Contains(detail, "▶ 11.0.0-preview.6") {
				t.Fatalf("detail version list:\n%s", detail)
			}
		})
	}
}
