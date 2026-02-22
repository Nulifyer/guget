package arger

import (
	"fmt"
	"os"
	"strings"
	"time"

	logger "logger"

	xterm "golang.org/x/term"
)

var registeredFlags = make(map[string]IFlag)
var aliasToFlag = make(map[string]IFlag)

// -------------------------------
// IFlag - interface for all flag types
// --------------------------------

type IFlag interface {
	GetName() string
	GetDescription() string
	GetRequired() bool
	GetAliases() []string
	GetPositional() bool
	GetFlagType() string
	GetDefault() any
	GetExpectedValues() []any
	parse(value string) (IParsedFlag, error)
	defaultParsed() IParsedFlag
}

// -------------------------------
// IParsedFlag - interface for all parsed flag types
// --------------------------------

type IParsedFlag interface {
	GetValue() any
	GetFlag() IFlag
}

// -------------------------------
// Flag - generic flag type
// --------------------------------

type Flag[T any] struct {
	Name           string
	Description    string
	Required       bool
	Default        *T
	DefaultFunc    func() T
	Aliases        []string
	Positional     bool
	ExpectedValues []T
	Parser         func(string) (T, error)
}

func (f Flag[T]) GetName() string        { return f.Name }
func (f Flag[T]) GetDescription() string { return f.Description }
func (f Flag[T]) GetRequired() bool      { return f.Required }
func (f Flag[T]) GetAliases() []string   { return f.Aliases }
func (f Flag[T]) GetPositional() bool    { return f.Positional }
func (f Flag[T]) GetFlagType() string    { return fmt.Sprintf("%T", *new(T)) }
func (f Flag[T]) GetDefault() any {
	if f.Default != nil {
		return *f.Default
	}
	if f.DefaultFunc != nil {
		return f.DefaultFunc()
	}
	return nil
}
func (f Flag[T]) GetExpectedValues() []any {
	out := make([]any, len(f.ExpectedValues))
	for i, v := range f.ExpectedValues {
		out[i] = v
	}
	return out
}

func (f Flag[T]) parse(value string) (IParsedFlag, error) {
	var v T
	if f.Parser == nil {
		_, err := fmt.Sscan(value, &v)
		if err != nil {
			flagError(f, "could not parse value %s", value)
		}
	} else {
		var err error
		v, err = f.Parser(value)
		if err != nil {
			flagError(f, "could not parse value %s", value)
		}
	}

	// validate expected values
	if len(f.ExpectedValues) > 0 {
		valid := false
		for _, ev := range f.ExpectedValues {
			if strings.EqualFold(fmt.Sprintf("%v", ev), fmt.Sprintf("%v", v)) {
				valid = true
				break
			}
		}
		if !valid {
			flagError(f, "invalid value %s", value)
		}
	}

	return ParsedFlag[T]{flag: &f, Value: v}, nil
}

func (f Flag[T]) defaultParsed() IParsedFlag {
	if f.Default != nil {
		return ParsedFlag[T]{flag: &f, Value: *f.Default}
	}
	if f.DefaultFunc != nil {
		return ParsedFlag[T]{flag: &f, Value: f.DefaultFunc()}
	}
	return nil
}

// -------------------------------
// ParsedFlag - generic parsed flag type
// --------------------------------

type ParsedFlag[T any] struct {
	flag  *Flag[T]
	Value T
}

func (pf ParsedFlag[T]) GetValue() any  { return pf.Value }
func (pf ParsedFlag[T]) GetFlag() IFlag { return pf.flag }
func (pf ParsedFlag[T]) As() T          { return pf.Value }

// -------------------------------
// Built-in flag constructors
// --------------------------------

func StringFlag(name string) Flag[string] {
	return Flag[string]{
		Name:   name,
		Parser: func(s string) (string, error) { return s, nil },
	}
}

func IntFlag(name string) Flag[int] {
	return Flag[int]{
		Name: name,
		Parser: func(s string) (int, error) {
			var v int
			_, err := fmt.Sscanf(s, "%d", &v)
			return v, err
		},
	}
}

func FloatFlag(name string) Flag[float64] {
	return Flag[float64]{
		Name: name,
		Parser: func(s string) (float64, error) {
			var v float64
			_, err := fmt.Sscanf(s, "%f", &v)
			return v, err
		},
	}
}

func BoolFlag(name string) Flag[bool] {
	return Flag[bool]{
		Name: name,
		Parser: func(s string) (bool, error) {
			switch strings.ToLower(s) {
			case "true", "1", "yes":
				return true, nil
			case "false", "0", "no":
				return false, nil
			default:
				return false, fmt.Errorf("invalid bool value: %s", s)
			}
		},
	}
}

func SwitchFlag(name string) Flag[bool] {
	return Flag[bool]{
		Name:   name,
		Parser: nil, // switches don't need a parser, value is set directly
	}
}

func DurationFlag(name string) Flag[time.Duration] {
	return Flag[time.Duration]{
		Name: name,
		Parser: func(s string) (time.Duration, error) {
			return time.ParseDuration(s)
		},
	}
}

// -------------------------------
// Register & Parse
// --------------------------------

func RegisterFlag(f IFlag) {
	validateFlag(f)

	registeredFlags[f.GetName()] = f
	for _, alias := range f.GetAliases() {
		if _, exists := aliasToFlag[alias]; exists {
			flagError(f, "Alias %s is already registered for another flag", alias)
		} else if alias == "--help" || alias == "-h" {
			flagError(f, "Alias %s is reserved for help flag", alias)
		} else if !strings.HasPrefix(alias, "--") && !strings.HasPrefix(alias, "-") {
			flagError(f, "Alias %s must start with - or -- per convention", alias)
		}
		aliasToFlag[alias] = f
	}
}

func validateFlag(f IFlag) {
	if f.GetName() == "" {
		flagError(f, "Flag name cannot be empty")
	}
	if _, exists := registeredFlags[f.GetName()]; exists {
		flagError(f, "Flag name %s is already registered", f.GetName())
	}
	if f.GetRequired() && f.GetDefault() != nil {
		flagError(f, "Flag --%s cannot be required and have a default value", f.GetName())
	}
	if len(f.GetAliases()) == 0 {
		flagError(f, "Flag --%s must have at least one alias", f.GetName())
	}
}

func Parse() map[string]IParsedFlag {
	if len(registeredFlags) == 0 && len(os.Args) > 1 {
		return nil
	}

	args := os.Args[1:]

	var (
		parsedFlags      = make(map[string]IParsedFlag)
		positionalValues []string
		lastFlag         IFlag
		isSwitch         bool
	)

	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			PrintUsage()
			os.Exit(0)
		} else if strings.HasPrefix(arg, "--") || strings.HasPrefix(arg, "-") {
			if mapped, exists := aliasToFlag[arg]; exists {
				lastFlag = mapped
				isSwitch = mapped.GetFlagType() == "bool" && mapped.(*Flag[bool]) != nil

				// handle switch flags directly
				if _, ok := lastFlag.(Flag[bool]); ok {
					if lastFlag.(Flag[bool]).Parser == nil {
						parsedFlags[lastFlag.GetName()] = ParsedFlag[bool]{flag: func() *Flag[bool] { f := lastFlag.(Flag[bool]); return &f }(), Value: true}
						lastFlag = nil
					}
				}
			} else {
				usageError("Unknown flag: %s", arg)
			}
		} else if lastFlag != nil {
			pf, err := lastFlag.parse(arg)
			if err != nil {
				flagError(lastFlag, "Failed to parse value. %s", err.Error())
			}
			parsedFlags[lastFlag.GetName()] = pf
			lastFlag = nil
		} else {
			positionalValues = append(positionalValues, arg)
		}
	}

	_ = isSwitch

	if lastFlag != nil {
		flagError(lastFlag, "Flag --%s expects a value but none was provided", lastFlag.GetName())
	}

	// handle positional args
	for _, value := range positionalValues {
		found := false
		for _, flag := range registeredFlags {
			if _, exists := parsedFlags[flag.GetName()]; !exists && flag.GetPositional() {
				pf, err := flag.parse(value)
				if err != nil {
					flagError(flag, "Failed to parse value. %s", err.Error())
				}
				parsedFlags[flag.GetName()] = pf
				found = true
				break
			}
		}
		if !found {
			usageError("Unexpected positional argument: %s", value)
		}
	}

	// apply defaults
	for _, flag := range registeredFlags {
		if _, exists := parsedFlags[flag.GetName()]; !exists {
			if def := flag.defaultParsed(); def != nil {
				parsedFlags[flag.GetName()] = def
			}
		}
	}

	// check required
	for _, flag := range registeredFlags {
		if flag.GetRequired() {
			if _, exists := parsedFlags[flag.GetName()]; !exists {
				flagError(flag, "Required flag not set")
			}
		}
	}

	aliasToFlag = nil
	registeredFlags = nil

	return parsedFlags
}

// -------------------------------
// Usage / Help
// --------------------------------

func PrintUsage() {
	fmt.Println("Usage:")

	termWidth, _, err := xterm.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		termWidth = 80
	}

	indent := 4
	leftColWidth := 0
	for name := range registeredFlags {
		if len(name) > leftColWidth {
			leftColWidth = len(name)
		}
	}
	if leftColWidth < 10 {
		leftColWidth = 10
	}
	leftColWidth += 2

	descWidth := termWidth - indent - leftColWidth - 1
	if descWidth < 20 {
		descWidth = 20
	}

	for _, f := range registeredFlags {
		aliases := strings.Join(f.GetAliases(), ", ")
		fmt.Printf("%s%-*s %s\n", strings.Repeat(" ", indent), leftColWidth, f.GetName(), aliases)
		if f.GetDescription() != "" {
			for _, ln := range wrapText(f.GetDescription(), descWidth) {
				fmt.Printf("%s%s\n", strings.Repeat(" ", indent+leftColWidth), ln)
			}
		}
		if len(f.GetExpectedValues()) > 0 {
			values := make([]string, len(f.GetExpectedValues()))
			for i, v := range f.GetExpectedValues() {
				values[i] = fmt.Sprintf("%v", v)
				if values[i] == "" {
					values[i] = "<empty>"
				}
			}
			fmt.Printf("%s[%s]\n", strings.Repeat(" ", indent+leftColWidth), strings.Join(values, ", "))
		}
		fmt.Println()
	}
}

func wrapText(s string, maxWidth int) []string {
	if s == "" || maxWidth <= 0 {
		return []string{}
	}
	var out []string
	words := strings.Fields(s)
	var line strings.Builder
	for i, w := range words {
		extra := 0
		if line.Len() > 0 {
			extra = 1
		}
		if line.Len()+len(w)+extra > maxWidth {
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
// Helper functions
// --------------------------------
func Optional[T any](v T) *T { return &v }

func usageError(format string, args ...any) {
	logger.Error(format, args...)
	PrintUsage()
	os.Exit(1)
}
func flagError(f IFlag, format string, args ...any) {
	logger.Error("error with flag %s (%s): %s", f.GetName(), strings.Join(f.GetAliases(), ", "), fmt.Sprintf(format, args...))
	PrintUsage()
	os.Exit(1)
}
func Get[T any](flags map[string]IParsedFlag, name string) T {
	pf, exists := flags[name]
	if !exists {
		logger.Fatal("Flag %s was not registered", name)
	}
	typed, ok := pf.(ParsedFlag[T])
	if !ok {
		logger.Fatal("Flag %s is not of expected type", name)
	}
	return typed.Value
}
