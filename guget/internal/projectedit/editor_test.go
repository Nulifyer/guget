package projectedit

import (
	"bytes"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nulifyer/guget/internal/edit"
)

func writeProject(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAddPreservesProjectStructure(t *testing.T) {
	tests := []struct{ name, content string }{
		{"compact", `<Project Sdk="Microsoft.NET.Sdk"><ItemGroup><PackageReference Include="Keep" Version="1.0.0" /></ItemGroup></Project>`},
		{"inline closing tags", "<Project>\n  <ItemGroup>\n    <PackageReference Include=\"Keep\" Version=\"1.0.0\" /></ItemGroup></Project>"},
		{"empty group", `<Project><ItemGroup /></Project>`},
		{"empty project", `<Project Sdk="Microsoft.NET.Sdk" />`},
		{"UTF-8 BOM", "\xef\xbb\xbf<Project Sdk=\"Microsoft.NET.Sdk\"><ItemGroup><PackageReference Include=\"Keep\" Version=\"1.0.0\" /></ItemGroup></Project>"},
		{"comments", `<Project><!-- </Project><ItemGroup><PackageReference Include="Fake" /></ItemGroup> --><ItemGroup><PackageReference Include="Keep" Version="1.0.0" /></ItemGroup></Project>`},
		{"CRLF", "<Project>\r\n\t<ItemGroup>\r\n\t\t<PackageReference Include=\"Keep\" Version=\"1.0.0\" />\r\n\t</ItemGroup>\r\n</Project>\r\n"},
		{"default namespace", `<Project xmlns="http://schemas.microsoft.com/developer/msbuild/2003"><ItemGroup><PackageReference Include="Keep" Version="1.0.0" /></ItemGroup></Project>`},
		{"prefixed namespace", `<m:Project xmlns:m="http://schemas.microsoft.com/developer/msbuild/2003"><m:ItemGroup><m:PackageReference Include="Keep" Version="1.0.0" /></m:ItemGroup></m:Project>`},
		{"conditional group", `<Project><ItemGroup Condition="'$(TargetFramework)' == 'net8.0'"><PackageReference Include="Keep" Version="1.0.0" /></ItemGroup></Project>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeProject(t, "App.csproj", tt.content)
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := AddPackageReference(path, "New.Package", "2.0.0"); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var project struct {
				XMLName xml.Name `xml:"Project"`
				Groups  []struct {
					Condition  string `xml:"Condition,attr"`
					References []struct {
						ID      string `xml:"Include,attr"`
						Version string `xml:"Version,attr"`
					} `xml:"PackageReference"`
				} `xml:"ItemGroup"`
			}
			if err := xml.Unmarshal(after, &project); err != nil {
				t.Fatalf("invalid project: %v\n%s", err, after)
			}
			found := 0
			for _, group := range project.Groups {
				for _, ref := range group.References {
					if ref.ID == "New.Package" {
						found++
						if ref.Version != "2.0.0" || group.Condition != "" {
							t.Fatalf("new reference=%+v condition=%q", ref, group.Condition)
						}
					}
				}
			}
			if found != 1 {
				t.Fatalf("new reference count=%d\n%s", found, after)
			}
			if strings.Contains(tt.content, "<!--") && !bytes.Contains(after, []byte(`<!-- </Project><ItemGroup><PackageReference Include="Fake" /></ItemGroup> -->`)) {
				t.Fatal("comment changed")
			}
			if strings.HasPrefix(tt.content, "\xef\xbb\xbf") && !bytes.HasPrefix(after, []byte("\xef\xbb\xbf")) {
				t.Fatal("UTF-8 BOM changed")
			}
			if strings.Contains(tt.content, "\r\n") && bytes.Contains(bytes.ReplaceAll(after, []byte("\r\n"), nil), []byte("\n")) {
				t.Fatal("introduced LF in CRLF document")
			}
			beforeAgain := append([]byte(nil), after...)
			if err := AddPackageReference(path, "new.package", "2.0.0"); err != nil {
				t.Fatal(err)
			}
			after, _ = os.ReadFile(path)
			if !bytes.Equal(after, beforeAgain) {
				t.Fatal("repeated add changed project")
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := info.Mode().Perm(), before.Mode().Perm(); got != want {
				t.Fatalf("permissions changed: got %o, want preserved mode %o", got, want)
			}
		})
	}
}

func TestAddRefusesInvalidXMLWithoutWriting(t *testing.T) {
	for _, content := range []string{`<Project><ItemGroup></Project>`, `<Project></Project><Project></Project>`, `<ItemGroup />`, `<Project><ItemGroup>`, `<Project/>outside`} {
		path := writeProject(t, "Bad.csproj", content)
		if err := AddPackageReference(path, "New", "1.0.0"); err == nil {
			t.Fatalf("accepted %q", content)
		}
		after, _ := os.ReadFile(path)
		if string(after) != content {
			t.Fatal("refused edit wrote file")
		}
	}
}

func TestBuildPlanDeduplicatesSharedCentralVersion(t *testing.T) {
	central := writeProject(t, "Directory.Packages.props", `<Project />`)
	first := writeProject(t, "First.csproj", `<Project />`)
	second := writeProject(t, "Second.csproj", `<Project />`)
	plan, err := BuildPlan(
		Operation{Kind: AddVersion, Path: central, Package: "Example", Version: "2.0.0"},
		Operation{Kind: AddReference, Path: first, Package: "Example"},
		Operation{Kind: AddVersion, Path: central, Package: "example", Version: "2.0.0"},
		Operation{Kind: AddReference, Path: second, Package: "Example"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Len() != 3 {
		t.Fatalf("plan len=%d", plan.Len())
	}
	if _, err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	centralData, _ := os.ReadFile(central)
	if bytes.Count(centralData, []byte("PackageVersion")) != 1 {
		t.Fatalf("central=%s", centralData)
	}
	for _, path := range []string{first, second} {
		data, _ := os.ReadFile(path)
		if bytes.Contains(data, []byte("Version=")) || !bytes.Contains(data, []byte(`Include="Example"`)) {
			t.Fatalf("project=%s", data)
		}
	}
}

func TestBuildPlanRefusesConflictOrStaleInput(t *testing.T) {
	path := writeProject(t, "App.csproj", `<Project><ItemGroup><PackageReference Include="Example" Version="1.0.0"/></ItemGroup></Project>`)
	if _, err := BuildPlan(Operation{Kind: UpdateVersion, Path: path, Package: "Example", Version: "2.0.0"}, Operation{Kind: UpdateVersion, Path: path, Package: "Example", Version: "3.0.0"}); err == nil {
		t.Fatal("accepted conflicting edits")
	}
	plan, err := BuildPlan(Operation{Kind: UpdateVersion, Path: path, Package: "Example", Version: "2.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	external := []byte(`<Project><!-- external --></Project>`)
	if err := os.WriteFile(path, external, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Apply(); !errors.Is(err, edit.ErrStale) {
		t.Fatalf("err=%v", err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, external) {
		t.Fatal("lost external edit")
	}
}
