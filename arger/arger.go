package arger

import (
	"fmt"
	"os"
	"strings"
)

type FlagType int

const (
	FlagTypeString FlagType = iota
	FlagTypeInt
	FlagTypeSwitch
)

type Flag struct {
	Name        string
	Description string
	Required    bool
	Default     string
	FlagType    FlagType
	Aliases     []string
	Set         bool
}

var registeredFlags []Flag

func RegisterFlag(name, description string, required bool, defaultValue string, flagType FlagType, flagAliases ...string) {
	f := Flag{
		Name:        name,
		Description: description,
		Required:    required,
		Default:     defaultValue,
		FlagType:    flagType,
		Aliases:     flagAliases,
		Set:         false,
	}
	registeredFlags = append(registeredFlags, f)
}

func PrintUsage() {
	fmt.Printf("Usage:")
	for _, flag := range registeredFlags {
		aliases := ""
		if len(flag.Aliases) > 0 {
			aliases = fmt.Sprintf(" (aliases: %s)", strings.Join(flag.Aliases, ", "))
		}
		fmt.Printf("  --%s%s: %s\n", flag.Name, aliases, flag.Description)
	}
}

func Parse() {
	os.Args = os.Args[1:]

	for pos, arg := range os.Args {

	}

	for _, flag := range registeredFlags {
		if flag.Required && !flag.Set {
			fmt.Printf("Required flag not set: --%s\n", flag.Name)
			PrintUsage()
			os.Exit(1)
		}
	}
}
