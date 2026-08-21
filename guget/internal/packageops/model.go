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
	Edit                EditSupport `json:"edit"`
}

type ProjectSnapshot struct {
	ProjectPath      string       `json:"projectPath"`
	TargetFrameworks []string     `json:"targetFrameworks"`
	PackageUses      []PackageUse `json:"packageUses"`
	Evaluated        bool         `json:"evaluated"`
	Warnings         []string     `json:"warnings"`
}
