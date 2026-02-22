package arger

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	logger "github.com/nulifyer/logger"
	xterm "golang.org/x/term"
)

var registeredFlags = make(map[string]Flag)
var aliasToFlag = make(map[string]*Flag)

func RegisterFlag(f Flag) {
	if f.Name == "" {
		logger.Fatal("Flag name cannot be empty")
	}
	if _, exists := registeredFlags[f.Name]; exists {
		logger.Fatal("Flag name %s is already registered", f.Name)
	}
	if f.Required && f.Default != "" {
		logger.Fatal("Flag --%s cannot be required and have a default value", f.Name)
	}
	if f.Aliases == nil || len(f.Aliases) == 0 {
		logger.Fatal("Flag --%s must have at least one alias", f.Name)
	}

	registeredFlags[f.Name] = f
	for _, alias := range f.Aliases {
		if _, exists := aliasToFlag[alias]; exists {
			logger.Fatal("Alias %s is already registered for another flag", alias)
		} else if alias == "--help" || alias == "-h" {
			logger.Fatal("Alias %s is reserved for help flag", alias)
		} else if !strings.HasPrefix(alias, "--") && !strings.HasPrefix(alias, "-") {
			logger.Fatal("Alias %s must start with - or -- per convention", alias)
		}
		aliasToFlag[alias] = &f
	}
}

func Parse() map[string]ParsedFlag {
	if (registeredFlags == nil || len(registeredFlags) == 0) && (len(os.Args) > 0) {
		return nil
	}

	os.Args = os.Args[1:]

	var (
		parsedFlags    = make(map[string]ParsedFlag)
		positionalValues []string
		lastParsedFlag *ParsedFlag
	)

	for pos, arg := range os.Args {
		if arg == "--help" || arg == "-h" {
			// help flag detected
			PrintUsage()
			os.Exit(0)
		} else if strings.HasPrefix(arg, "--") || strings.HasPrefix(arg, "-") {
			// arg flag detected
			if mapped_flag, exists := aliasToFlag[arg]; exists {
				lastParsedFlag = &ParsedFlag{Flag: mapped_flag, Type: mapped_flag.FlagType}
				parsedFlags[mapped_flag.Name] = *lastParsedFlag

				if lastParsedFlag.Type == FlagTypeSwitch {
					lastParsedFlag.Value = true
					lastParsedFlag = nil
				}
			} else {
				logger.Fatal("Unknown flag: %s", arg)
			}
		} else if lastParsedFlag != nil {
			// value for the last parsed flag
			lastParsedFlag.Value = ParseFlagValue(lastParsedFlag.Flag, arg)
			lastParsedFlag = nil
		} else {
			// value without a preceding flag, save for later processing
			positionalValues = append(positionalValues, arg)
		}
	}

	if lastParsedFlag != nil {
		logger.Fatal("Flag --%s expects a value but none was provided", lastParsedFlag.Flag.Name)
	}
	
	var foundPositionalFlag bool
	lastParsedFlag = nil
	for i, value := range positionalValues {
		foundPositionalFlag = false
		for _, flag := range registeredFlags {
			if _, exists := parsedFlags[flag.Name]; !exists && flag.Positional == true {
				lastParsedFlag = &ParsedFlag{Flag: &flag, Type: flag.FlagType}
				parsedFlags[flag.Name] = *lastParsedFlag
				foundPositionalFlag = true
				break
			}
		}
		if foundPositionalFlag {
	}

	// add default values
	for _, flag := range registeredFlags {
		if _, exists := parsedFlags[flag.Name]; !exists && flag.Default != "" {
			parsedFlags[flag.Name] = ParsedFlag{
				Flag:  &flag,
				Type:  flag.FlagType,
				Value: ParseFlagValue(&flag, flag.Default),
			}
		}
	}

	for _, flag := range registeredFlags {
		if flag.Required && !flag.Set {
			logger.Fatal("Required flag not set: --%s", flag.Name)
			PrintUsage()
			os.Exit(1)
		}
	}

	aliasToFlag = nil
	registeredFlags = nil

	return parsedFlags
}

// -------------------------------
// Helper functions
// --------------------------------

func ParseFlagValue(flag *Flag, value string) any {
	switch flag.FlagType {
	case FlagTypeString:
		return value
	case FlagTypeInt:
		var intValue int
		_, err := fmt.Sscanf(value, "%d", &intValue)
		if err != nil {
			logger.Fatal("Invalid value for flag --%s: expected int, got %s", flag.Name, value)
		}
		return intValue
	case FlagTypeFloat:
		var floatValue float64
		_, err := fmt.Sscanf(value, "%f", &floatValue)
		if err != nil {
			logger.Fatal("Invalid value for flag --%s: expected float, got %s", flag.Name, value)
		}
		return floatValue
	case FlagTypeDuration:
		durationValue, err := time.ParseDuration(value)
		if err != nil {
			logger.Fatal("Invalid value for flag --%s: expected duration, got %s", flag.Name, value)
		}
		return durationValue
	}
	return nil
}

func PrintUsage() {
	fmt.Println("Usage:")

	names := make([]string, 0, len(registeredFlags))
	for name := range registeredFlags {
		names = append(names, name)
	}

	// determine terminal width for formatting
	termWidth, _, err := xterm.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		termWidth = 80 // default width if we can't get terminal size
	}

	indent := 4
	leftColWidth := 0
	for _, name := range names {
		l := len(name)
		if l > leftColWidth { leftColWidth = l }
	}
	if leftColWidth < 10 { leftColWidth = 10 }
	leftColWidth += 2 // padding

	descWidth := termWidth - indent - leftColWidth - 1
	if descWidth < 20 { descWidth = 20 }

	// layout: indented flag name column, aliases column, description on next line under aliases
	for _, name := range names {
		f := registeredFlags[name]
		aliases := strings.Join(f.Aliases, ", ")
		// first line: flag name and aliases
		fmt.Printf("%s%-*s %s\n", strings.Repeat(" ", indent), leftColWidth, f.Name, aliases)
		// description lines (wrapped)
		if f.Description != "" {
			lines := wrapText(f.Description, descWidth)
			for _, ln := range lines {
				fmt.Printf("%s%s\n", strings.Repeat(" ", indent+leftColWidth), ln)
			}
		}
		fmt.Println()
	}
}

// wrapText splits s into lines with maxWidth characters (simple rune-aware wrap)
func wrapText(s string, maxWidth int) []string {
	if s == "" || maxWidth <= 0 {
		return []string{}
	}
	var out []string
	words := strings.Fields(s)
	var line strings.Builder
	for i, w := range words {
		if line.Len()+len(w) + func() int { if line.Len() > 0 { return 1 } ; return 0 }() > maxWidth {
			out = append(out, line.String())
			line.Reset()
		}
		if line.Len() > 0 {
			line.WriteByte(' ')
		}
		line.WriteString(w)
		if i == len(words)-1 {
			out = append(out, line.String())
		}
	}
	return out
}

// -------------------------------
// Flag - provides information about a registered flag.
// --------------------------------

type FlagType int
const (
	FlagTypeInt FlagType = iota
	FlagTypeFloat
	FlagTypeSwitch
	FlagTypeString
	FlagTypeDuration
)

type Flag struct {
	Name        string
	Description string = ""
	Required    bool = false
	FlagType    FlagType = FlagTypeSwitch
	Default     string = ""
	Aliases     []string
	Positional  bool = false
}

func (f *Flag) AsString() string {
	return f.Name
}
func (f *Flag) AsStringWithAliases() string {
	aliasStr := strings.Join(f.Aliases, " ")
	out := fmt.Sprintf("%s (%s)", f.Name, aliasStr)
	return out
}
func (f *Flag) AsStringFull() string {
	aliasStr := strings.Join(f.Aliases, " ")
	out := fmt.Sprintf("%s (%s): %s", f.Name, aliasStr, f.Description)
	return out
}

// -------------------------------
// ParsedFlag - provides type-safe access to the value of a parsed flag.
// --------------------------------

type ParsedFlag struct {
	Flag  *Flag
	Type  FlagType
	Value any
}

func (pf *ParsedFlag) AsString() string {
	if pf.Type != FlagTypeString {
		logger.Fatal("Flag %s is not of type string", pf.Flag.Name)
	}
	return pf.Value.(string)
}

func (pf *ParsedFlag) AsInt() int {
	if pf.Type != FlagTypeInt {
		logger.Fatal("Flag %s is not of type int", pf.Flag.Name)
	}
	return pf.Value.(int)
}

func (pf *ParsedFlag) AsBool() bool {
	if pf.Type != FlagTypeSwitch {
		logger.Fatal("Flag %s is not of type bool", pf.Flag.Name)
	}
	return pf.Value.(bool)
}

func (pf *ParsedFlag) AsFloat() float64 {
	if pf.Type != FlagTypeFloat {
		logger.Fatal("Flag %s is not of type float", pf.Flag.Name)
	}
	return pf.Value.(float64)
}

func (pf *ParsedFlag) AsDuration() time.Duration {
	if pf.Type != FlagTypeDuration {
		logger.Fatal("Flag %s is not of type duration", pf.Flag.Name)
	}
	return pf.Value.(time.Duration)
}
