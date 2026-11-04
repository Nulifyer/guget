package projectedit

import (
	"strings"
	"testing"
)

func TestValidateVersion(t *testing.T) {
	for _, version := range []string{"1", "1.2", "1.2.3.4", "1.2.3-preview.1+build.7", "[1.2.3]", "[1.0,2.0)", "(,2.0]", "[1.0,)", "1.*", "*", "1.2.3-*", "1.2.3-preview.*"} {
		if err := ValidateVersion(version); err != nil {
			t.Errorf("valid %q: %v", version, err)
		}
	}
	for _, version := range []string{"", "latest", "garbage", "1..2", "1.2.3.4.5", "1.2.3-", " 1.2.3", "$(Version)", "[invalid]", "(1.0)", "[,2.0]", "[1.0,]", "1.2.3\" />"} {
		if err := ValidateVersion(version); err == nil {
			t.Errorf("accepted invalid %q", version)
		}
	}
}

func TestConditionalSourceIsReadOnly(t *testing.T) {
	for _, content := range []string{
		`<Project><ItemGroup><PackageReference Include="Example"><Version Condition="false">1.0.0</Version></PackageReference></ItemGroup></Project>`,
		`<Project><ItemGroup Condition="false"><PackageReference Include="Example" Version="1.0.0" /></ItemGroup></Project>`,
		`<Project><ItemGroup><PackageReference Condition="'$(TargetFramework)' == 'net8.0'" Include="Example" Version="1.0.0" /></ItemGroup></Project>`,
	} {
		path := writeProject(t, "App.csproj", content)
		if _, err := PlanUpdatePackageVersion(path, "Example", "2.0.0"); err == nil {
			t.Fatal("conditional update accepted")
		}
		if _, err := PlanRemovePackageReference(path, "Example"); err == nil {
			t.Fatal("conditional removal accepted")
		}
		if _, err := LiteralVersion(path, "Example"); err == nil {
			t.Fatal("conditional source reported editable")
		}
	}
}

func TestChildVersionOverrideTakesPrecedenceOverVersionAttribute(t *testing.T) {
	path := writeProject(t, "App.csproj", `<Project><ItemGroup><PackageReference Include="Example" Version="1.0.0"><VersionOverride>2.0.0</VersionOverride></PackageReference></ItemGroup></Project>`)
	version, err := LiteralVersion(path, "Example")
	if err != nil || version != "2.0.0" {
		t.Fatalf("version=%q err=%v", version, err)
	}
	change, err := PlanUpdatePackageVersion(path, "Example", "3.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(change.After), `Version="1.0.0"`) || !strings.Contains(string(change.After), `<VersionOverride>3.0.0</VersionOverride>`) {
		t.Fatalf("result=%s", change.After)
	}
}
