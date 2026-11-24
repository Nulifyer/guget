package msbuildstatic

import "testing"

func TestImportPathUsesOnlyReservedProperties(t *testing.T) {
	root := writeTree(t, map[string]string{"Directory.Packages.props": `<Project />`, "src/Directory.Packages.props": `<Project />`})
	declaring := path(root, "src/Directory.Packages.props")
	project := path(root, "src/App/App.csproj")
	for raw, want := range map[string]string{
		"imported.props": path(root, "src/imported.props"),
		`$(MSBuildThisFileDirectory)\common.props`:                                                  path(root, "src/common.props"),
		`$(MSBuildProjectDirectory)/../x.props`:                                                     path(root, "src/x.props"),
		`$([MSBuild]::GetPathOfFileAbove(Directory.Packages.props, $(MSBuildThisFileDirectory)..))`: path(root, "Directory.Packages.props"),
		`$([MSBuild]::GetPathOfFileAbove('Directory.Packages.props'))`:                              declaring,
	} {
		if got, ok := ImportPath(raw, declaring, project); !ok || got != want {
			t.Errorf("ImportPath(%q) = %q, %v; want %q", raw, got, ok, want)
		}
	}
	// $(ProjectDir) is defined by the common targets, after project evaluation.
	for _, raw := range []string{`$(ProjectDir)imported.props`, `$(SomeCustomVar)\file.props`, `*.props`} {
		if got, ok := ImportPath(raw, declaring, project); ok {
			t.Errorf("ImportPath(%q) = %q; want unresolved", raw, got)
		}
	}
}
