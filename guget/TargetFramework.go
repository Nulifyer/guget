package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type FrameworkFamily string

const (
	FamilyNet      FrameworkFamily = "net"         // net5.0, net6.0, net8.0 etc
	FamilyNetFx    FrameworkFamily = "netfx"       // net45, net472 etc (legacy)
	FamilyStandard FrameworkFamily = "netstandard" // netstandard2.0, netstandard2.1
	FamilyCoreApp  FrameworkFamily = "netcoreapp"  // netcoreapp3.1 etc
	FamilyUnknown  FrameworkFamily = "unknown"
)

type TargetFramework struct {
	Raw    string
	Family FrameworkFamily
	Major  int
	Minor  int
}

var (
	reNet      = regexp.MustCompile(`^net(\d+)\.(\d+)$`)         // net6.0, net8.0
	reNetFx    = regexp.MustCompile(`^net(\d)(\d+)$`)            // net45, net472, net48
	reStandard = regexp.MustCompile(`^netstandard(\d+)\.(\d+)$`) // netstandard2.0
	reCoreApp  = regexp.MustCompile(`^netcoreapp(\d+)\.(\d+)$`)  // netcoreapp3.1
)

func ParseTargetFramework(raw string) TargetFramework {
	s := strings.ToLower(strings.TrimSpace(raw))

	// net6.0, net8.0, net9.0
	if m := reNet.FindStringSubmatch(s); m != nil {
		return TargetFramework{Raw: raw, Family: FamilyNet, Major: atoi(m[1]), Minor: atoi(m[2])}
	}
	// netstandard2.0, netstandard2.1
	if m := reStandard.FindStringSubmatch(s); m != nil {
		return TargetFramework{Raw: raw, Family: FamilyStandard, Major: atoi(m[1]), Minor: atoi(m[2])}
	}
	// netcoreapp3.1
	if m := reCoreApp.FindStringSubmatch(s); m != nil {
		return TargetFramework{Raw: raw, Family: FamilyCoreApp, Major: atoi(m[1]), Minor: atoi(m[2])}
	}
	// net45, net472, net48
	if m := reNetFx.FindStringSubmatch(s); m != nil {
		major := atoi(m[1])
		minor := atoi(m[2])
		return TargetFramework{Raw: raw, Family: FamilyNetFx, Major: major, Minor: minor}
	}

	return TargetFramework{Raw: raw, Family: FamilyUnknown}
}

// IsNewerThan returns true if tf is a strictly newer version than other within the same family.
func (tf TargetFramework) IsNewerThan(other TargetFramework) bool {
	if tf.Family != other.Family {
		return false
	}
	if tf.Major != other.Major {
		return tf.Major > other.Major
	}
	return tf.Minor > other.Minor
}

// IsCompatibleWith returns true if this framework can consume a package
// targeting 'required'. Compatibility rules mirror NuGet's:
//   - net X.Y is compatible with netstandard <= 2.1, netcoreapp, and older net
//   - netstandard X.Y is compatible with netstandard <= X.Y
//   - "any" / empty means compatible with everything
func (tf TargetFramework) IsCompatibleWith(other TargetFramework) bool {
	if other.Family == FamilyUnknown || other.Raw == "any" || other.Raw == "" {
		return true
	}

	switch other.Family {

	case FamilyNet:
		// package requires net X.Y — project must be >= that version
		return tf.Family == FamilyNet &&
			(tf.Major > other.Major ||
				(tf.Major == other.Major && tf.Minor >= other.Minor))

	case FamilyStandard:
		// netstandard is consumable by net5+, netcoreapp, netfx (if high enough), and netstandard (if high enough)
		switch tf.Family {
		case FamilyNet:
			return tf.Major >= 5 // net5+ supports all netstandard
		case FamilyCoreApp:
			return true // netcoreapp supports netstandard
		case FamilyStandard:
			return tf.Major > other.Major ||
				(tf.Major == other.Major && tf.Minor >= other.Minor)
		case FamilyNetFx:
			// net462+ supports netstandard2.0, net47+ supports more
			return other.Major == 1 ||
				(other.Major == 2 && other.Minor == 0 && tf.Minor >= 62)
		}

	case FamilyCoreApp:
		return tf.Family == FamilyCoreApp &&
			(tf.Major > other.Major ||
				(tf.Major == other.Major && tf.Minor >= other.Minor))

	case FamilyNetFx:
		return tf.Family == FamilyNetFx &&
			(tf.Major > other.Major ||
				(tf.Major == other.Major && tf.Minor >= other.Minor))
	}

	return false
}

func (tf TargetFramework) String() string {
	if tf.Raw != "" {
		return tf.Raw
	}
	return fmt.Sprintf("%s%d.%d", tf.Family, tf.Major, tf.Minor)
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
