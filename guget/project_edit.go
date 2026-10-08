package main

import (
	"github.com/nulifyer/guget/internal/projectedit"
)

func RemovePackageReference(filePath, pkgName string) error {
	return projectedit.RemovePackageReference(filePath, pkgName)
}

func UpdatePackageVersion(filePath, pkgName, newVersion string) error {
	return projectedit.UpdatePackageVersion(filePath, pkgName, newVersion)
}

func AddPackageReference(filePath, pkgName, version string) error {
	return projectedit.AddPackageReference(filePath, pkgName, version)
}

func AddPackageVersion(filePath, pkgName, version string) error {
	return projectedit.AddPackageVersion(filePath, pkgName, version)
}
