package main

import (
	"fmt"
	"sort"
	"strings"
)

var completionShells = []string{"bash", "zsh", "fish"}

func completionScript(shell string) (string, bool) {
	switch shell {
	case "bash":
		return bashCompletion(), true
	case "zsh":
		return zshCompletion(), true
	case "fish":
		return fishCompletion(), true
	}
	return "", false
}

// hasPositionalChoices reports whether any positional completes from a list.
func hasPositionalChoices(spec *cliVerbSpec) bool {
	for _, positional := range spec.Positionals {
		if positional.Kind == valueChoice {
			return true
		}
	}
	return false
}

func verbValueFlagWords(spec *cliVerbSpec) []string {
	var words []string
	for _, flag := range spec.Flags {
		if flag.takesValue() {
			words = append(words, "--"+flag.Name)
		}
	}
	return words
}

func verbFlagWords(spec *cliVerbSpec) []string {
	var words []string
	for _, flag := range spec.Flags {
		words = append(words, "--"+flag.Name)
		if flag.Short != "" {
			words = append(words, "-"+flag.Short)
		}
	}
	return words
}

// tuiCompletionFlags are the long TUI options offered before a verb.
func tuiCompletionFlags() []string {
	flags := []string{"--help", "--version", "--project", "--theme", "--sort-by", "--no-mouse", "--no-color", "--log-file", "--verbose", "--no-update-check", "--" + includeIgnoredFlag}
	sort.Strings(flags)
	return flags
}

// bashCompletion completes verbs, each verb's flags, and choice values. Flags
// are offered only when the current word starts with "-"; otherwise the
// function returns no candidates and `complete -o default` falls back to
// file and directory completion for values such as --project and --output.
func bashCompletion() string {
	var b strings.Builder
	valueFlags := make([]string, 0)
	for name := range cliGlobalValueFlags() {
		valueFlags = append(valueFlags, "--"+name)
	}
	sort.Strings(valueFlags)
	b.WriteString("# bash completion for guget\n_guget() {\n")
	b.WriteString("  local cur prev verb vi i j word pos last\n")
	b.WriteString("  cur=\"${COMP_WORDS[COMP_CWORD]}\"\n")
	b.WriteString("  prev=\"${COMP_WORDS[COMP_CWORD-1]}\"\n")
	b.WriteString("  COMPREPLY=()\n")
	b.WriteString("  verb=\"\"\n")
	b.WriteString("  for ((i = 1; i < COMP_CWORD; i++)); do\n")
	b.WriteString("    word=\"${COMP_WORDS[i]}\"\n")
	b.WriteString("    case \"$word\" in\n")
	// COMP_WORDBREAKS splits --flag=value into --flag, =, value.
	fmt.Fprintf(&b, "      %s) if [[ \"${COMP_WORDS[i+1]}\" == \"=\" ]]; then ((i += 2)); else ((i++)); fi ;;\n", strings.Join(valueFlags, "|"))
	b.WriteString("      =) ((i++)) ;;\n")
	b.WriteString("      -*) ;;\n")
	b.WriteString("      *) verb=\"$word\"; vi=$i; break ;;\n")
	b.WriteString("    esac\n")
	b.WriteString("  done\n")
	// A value after "--flag=" arrives as its own word following "=".
	b.WriteString("  if [[ \"$prev\" == \"=\" && $COMP_CWORD -ge 2 ]]; then prev=\"${COMP_WORDS[COMP_CWORD-2]}\"; fi\n")
	b.WriteString("  if [[ -z \"$verb\" ]]; then\n")
	fmt.Fprintf(&b, "    if [[ \"$cur\" == -* ]]; then COMPREPLY=($(compgen -W %q -- \"$cur\")); return 0; fi\n", strings.Join(tuiCompletionFlags(), " "))
	b.WriteString("    case \"$prev\" in\n")
	fmt.Fprintf(&b, "      %s) return 0 ;;\n", strings.Join(valueFlags, "|"))
	b.WriteString("    esac\n")
	fmt.Fprintf(&b, "    COMPREPLY=($(compgen -W %q -- \"$cur\"))\n", strings.Join(cliVerbNames(), " "))
	b.WriteString("    return 0\n")
	b.WriteString("  fi\n")
	b.WriteString("  case \"$verb\" in\n")
	for _, spec := range cliVerbsInOrder() {
		fmt.Fprintf(&b, "    %s)\n", spec.Name)
		var choiceCases []string
		for _, flag := range spec.Flags {
			if flag.Kind == valueChoice {
				choiceCases = append(choiceCases, fmt.Sprintf("        --%s) COMPREPLY=($(compgen -W %q -- \"$cur\")); return 0 ;;\n", flag.Name, strings.Join(flag.Choices, " ")))
			}
		}
		if len(choiceCases) > 0 {
			b.WriteString("      case \"$prev\" in\n")
			for _, line := range choiceCases {
				b.WriteString(line)
			}
			b.WriteString("      esac\n")
		}
		fmt.Fprintf(&b, "      if [[ \"$cur\" == -* ]]; then COMPREPLY=($(compgen -W %q -- \"$cur\")); return 0; fi\n", strings.Join(verbFlagWords(spec), " "))
		if hasPositionalChoices(spec) {
			writeBashPositionals(&b, spec)
		}
		b.WriteString("      ;;\n")
	}
	b.WriteString("  esac\n")
	b.WriteString("  return 0\n")
	b.WriteString("}\n")
	b.WriteString("complete -o default -F _guget guget\n")
	return b.String()
}

// writeBashPositionals counts the positionals before the current word, skipping
// flags and their values, and completes the choices for that position.
func writeBashPositionals(b *strings.Builder, spec *cliVerbSpec) {
	b.WriteString("      pos=0; last=\"\"\n")
	b.WriteString("      for ((j = vi + 1; j < COMP_CWORD; j++)); do\n")
	b.WriteString("        word=\"${COMP_WORDS[j]}\"\n")
	b.WriteString("        case \"$word\" in\n")
	if values := verbValueFlagWords(spec); len(values) > 0 {
		fmt.Fprintf(b, "          %s) if [[ \"${COMP_WORDS[j+1]}\" == \"=\" ]]; then ((j += 2)); else ((j++)); fi ;;\n", strings.Join(values, "|"))
	}
	b.WriteString("          =) ((j++)) ;;\n")
	b.WriteString("          -*) ;;\n")
	b.WriteString("          *) last=\"$word\"; ((pos++)) ;;\n")
	b.WriteString("        esac\n")
	b.WriteString("      done\n")
	b.WriteString("      case $pos in\n")
	for i, positional := range spec.Positionals {
		if positional.Kind != valueChoice {
			continue
		}
		reply := fmt.Sprintf("COMPREPLY=($(compgen -W %q -- \"$cur\"))", strings.Join(positional.choices(), " "))
		if len(positional.After) > 0 {
			fmt.Fprintf(b, "        %d) case \"$last\" in %s) %s ;; esac ;;\n", i, strings.Join(positional.After, "|"), reply)
		} else {
			fmt.Fprintf(b, "        %d) %s ;;\n", i, reply)
		}
	}
	b.WriteString("      esac\n")
}

func zshQuote(text string) string {
	return strings.NewReplacer("'", "'\\''", "[", "\\[", "]", "\\]", ":", "\\:").Replace(text)
}

func zshFlagSpec(flag cliFlagSpec) string {
	prefix := ""
	if flag.Repeatable {
		prefix = "*"
	}
	description := zshQuote(flag.Description)
	names := "--" + flag.Name
	if flag.Short != "" {
		names = "{-" + flag.Short + ",--" + flag.Name + "}"
		return fmt.Sprintf("    '(-%s --%s)'%s'[%s]' \\\n", flag.Short, flag.Name, names, description)
	}
	if !flag.takesValue() {
		return fmt.Sprintf("    '%s%s[%s]' \\\n", prefix, names, description)
	}
	action := ""
	switch flag.Kind {
	case valueFile:
		action = "_files"
	case valueDir:
		action = "_files -/"
	case valueChoice:
		action = "(" + strings.Join(flag.Choices, " ") + ")"
	}
	return fmt.Sprintf("    '%s%s=[%s]:%s:%s' \\\n", prefix, names, description, strings.ToLower(flag.Value), action)
}

func zshCompletion() string {
	var b strings.Builder
	b.WriteString("#compdef guget\n\n_guget() {\n")
	b.WriteString("  local curcontext=\"$curcontext\" state line\n")
	b.WriteString("  local -a commands\n")
	b.WriteString("  commands=(\n")
	for _, spec := range cliVerbsInOrder() {
		fmt.Fprintf(&b, "    '%s:%s'\n", spec.Name, zshQuote(spec.Summary))
	}
	b.WriteString("  )\n")
	b.WriteString("  _arguments -C \\\n")
	for _, flag := range []cliFlagSpec{flagProject, flagIncludeIgnored, flagNoColor} {
		b.WriteString(zshFlagSpec(flag))
	}
	b.WriteString("    '1: :->command' \\\n")
	b.WriteString("    '*:: :->args'\n")
	b.WriteString("  case $state in\n")
	b.WriteString("    command)\n")
	b.WriteString("      _describe -t commands 'guget command' commands\n")
	b.WriteString("      _files -/\n")
	b.WriteString("      ;;\n")
	b.WriteString("    args)\n")
	b.WriteString("      case $words[1] in\n")
	for _, spec := range cliVerbsInOrder() {
		fmt.Fprintf(&b, "        %s)\n", spec.Name)
		b.WriteString("          _arguments \\\n")
		for _, flag := range spec.Flags {
			b.WriteString("  " + zshFlagSpec(flag))
		}
		for i, positional := range spec.Positionals {
			action := " "
			if positional.Kind == valueChoice {
				action = "(" + strings.Join(positional.choices(), " ") + ")"
				if len(positional.After) > 0 && i > 0 {
					// words[1] is the verb, so the previous positional is words[i+1].
					action = fmt.Sprintf("{[[ ${words[%d]} == (%s) ]] && compadd -- %s}", i+1, strings.Join(positional.After, "|"), strings.Join(positional.choices(), " "))
				}
			}
			optional := ""
			if positional.Optional {
				optional = ":"
			}
			fmt.Fprintf(&b, "      '%d:%s%s:%s' \\\n", i+1, optional, strings.ToLower(positional.Name), action)
		}
		b.WriteString("            && return\n")
		b.WriteString("          ;;\n")
	}
	b.WriteString("      esac\n")
	b.WriteString("      ;;\n")
	b.WriteString("  esac\n")
	b.WriteString("}\n\n")
	b.WriteString("if [ \"$funcstack[1]\" = \"_guget\" ]; then\n  _guget \"$@\"\nelse\n  compdef _guget guget\nfi\n")
	return b.String()
}

func fishQuote(text string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, "'", `\'`).Replace(text) + "'"
}

func fishCompletion() string {
	var b strings.Builder
	b.WriteString("# fish completion for guget\n")
	for _, spec := range cliVerbsInOrder() {
		fmt.Fprintf(&b, "complete -c guget -f -n '__fish_use_subcommand' -a %s -d %s\n", spec.Name, fishQuote(spec.Summary))
	}
	for _, spec := range cliVerbsInOrder() {
		condition := fmt.Sprintf("'__fish_seen_subcommand_from %s'", spec.Name)
		for i, positional := range spec.Positionals {
			if positional.Kind != valueChoice {
				continue
			}
			when := "__fish_seen_subcommand_from " + spec.Name
			switch {
			case len(positional.After) > 0:
				when += "; and __fish_seen_subcommand_from " + strings.Join(positional.After, " ")
			case i == 0:
				var others []string
				for _, choice := range positional.choices() {
					if choice != spec.Name {
						others = append(others, choice)
					}
				}
				when += "; and not __fish_seen_subcommand_from " + strings.Join(others, " ")
			}
			fmt.Fprintf(&b, "complete -c guget -f -n '%s' -a %s\n", when, fishQuote(strings.Join(positional.choices(), " ")))
		}
		for _, flag := range spec.Flags {
			line := fmt.Sprintf("complete -c guget -n %s -l %s", condition, flag.Name)
			if flag.Short != "" {
				line += " -s " + flag.Short
			}
			if flag.takesValue() {
				switch flag.Kind {
				case valueChoice:
					line += " -x -a " + fishQuote(strings.Join(flag.Choices, " "))
				case valueDir:
					line += " -x -a '(__fish_complete_directories)'"
				case valueFile:
					line += " -r -F"
				default:
					line += " -x"
				}
			}
			line += " -d " + fishQuote(flag.Description)
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}
