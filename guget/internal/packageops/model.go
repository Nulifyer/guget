// Package packageops defines UI-independent package inspection results.
package packageops

type EditSupport struct {
	Supported bool   `json:"supported"`
	Reason    string `json:"reason,omitempty"`
}

type PackageUse struct {
	ProjectPath         string      `json:"projectPath"`
	TargetFramework     string      `json:"targetFramework"`
	PackageID           string      `json:"packageId"`
	EvaluatedExpression string      `json:"evaluatedExpression,omitempty"`
	ResolvedVersion     string      `json:"resolvedVersion,omitempty"`
	ReferenceOwner      string      `json:"referenceOwner"`
	VersionOwner        string      `json:"versionOwner,omitempty"`
	Direct              bool        `json:"direct"`
	Implicit            bool        `json:"implicit"`
	Global              bool        `json:"global,omitempty"`        // from a GlobalPackageReference
	PrivateAssets       string      `json:"privateAssets,omitempty"` // "All" for a GlobalPackageReference
	Edit                EditSupport `json:"edit"`
}

// PackageDownload is a restore-only PackageDownload item. It is not a
// dependency and is never centrally managed.
type PackageDownload struct {
	ProjectPath     string `json:"projectPath"`
	TargetFramework string `json:"targetFramework"`
	PackageID       string `json:"packageId"`
	Version         string `json:"version"`
	Owner           string `json:"owner"`
}

type ProjectSnapshot struct {
	CentralPackageManagement bool              `json:"centralPackageManagement"`
	ProjectPath              string            `json:"projectPath"`
	TargetFrameworks         []string          `json:"targetFrameworks"`
	PackageUses              []PackageUse      `json:"packageUses"`
	PackageDownloads         []PackageDownload `json:"packageDownloads,omitempty"`
	Evaluated                bool              `json:"evaluated"`
	Warnings                 []string          `json:"warnings"`
}
