package msbuildstatic

import (
	"os"
	"strconv"
	"strings"
)

// truth is a three-state condition result. Unknown conditions depend on
// values the static evaluator cannot see.
type truth int

const (
	unknown truth = iota
	isFalse
	isTrue
)

func (t truth) and(other truth) truth {
	switch {
	case t == isFalse || other == isFalse:
		return isFalse
	case t == isTrue && other == isTrue:
		return isTrue
	}
	return unknown
}

func (t truth) or(other truth) truth {
	switch {
	case t == isTrue || other == isTrue:
		return isTrue
	case t == isFalse && other == isFalse:
		return isFalse
	}
	return unknown
}

func (t truth) not() truth {
	switch t {
	case isTrue:
		return isFalse
	case isFalse:
		return isTrue
	}
	return unknown
}

// evaluateCondition evaluates an MSBuild condition. Relative Exists() paths are
// resolved against baseDir: the declaring file's directory for property groups
// and imports, and the project directory for items (msbuild Evaluator.cs
// GetCurrentDirectoryForConditionEvaluation).
func (s scope) evaluateCondition(condition, baseDir string) truth {
	if strings.TrimSpace(condition) == "" {
		return isTrue
	}
	parser := conditionParser{scope: s, baseDir: baseDir, tokens: tokenizeCondition(condition)}
	result, ok := parser.parseOr()
	if !ok || parser.pos != len(parser.tokens) {
		return unknown
	}
	return result
}

type conditionToken struct {
	kind  byte // 's' string, 'w' bare word, 'o' operator, '(' or ')', ','
	text  string
	quote bool
}

func tokenizeCondition(s string) []conditionToken {
	var tokens []conditionToken
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++
		case c == '(' || c == ')' || c == ',':
			tokens = append(tokens, conditionToken{kind: c, text: string(c)})
			i++
		case c == '\'':
			// A quoted string can hold $(...) with its own quotes.
			j := i + 1
			for j < len(s) && s[j] != '\'' {
				if (s[j] == '$' || s[j] == '@' || s[j] == '%') && j+1 < len(s) && s[j+1] == '(' {
					if end := matchParen(s, j+1); end > 0 {
						j = end + 1
						continue
					}
				}
				j++
			}
			if j >= len(s) {
				return append(tokens, conditionToken{kind: '?'})
			}
			tokens = append(tokens, conditionToken{kind: 's', text: s[i+1 : j], quote: true})
			i = j + 1
		case strings.HasPrefix(s[i:], "==") || strings.HasPrefix(s[i:], "!=") || strings.HasPrefix(s[i:], "<=") || strings.HasPrefix(s[i:], ">="):
			tokens = append(tokens, conditionToken{kind: 'o', text: s[i : i+2]})
			i += 2
		case c == '<' || c == '>' || c == '!':
			tokens = append(tokens, conditionToken{kind: 'o', text: string(c)})
			i++
		default:
			j := i
			for j < len(s) {
				if (s[j] == '$' || s[j] == '@' || s[j] == '%') && j+1 < len(s) && s[j+1] == '(' {
					if end := matchParen(s, j+1); end > 0 {
						j = end + 1
						continue
					}
				}
				if strings.ContainsRune(" \t\r\n()',=!<>", rune(s[j])) {
					break
				}
				j++
			}
			if j == i {
				return append(tokens, conditionToken{kind: '?'})
			}
			tokens = append(tokens, conditionToken{kind: 'w', text: s[i:j]})
			i = j
		}
	}
	return tokens
}

type conditionParser struct {
	scope   scope
	baseDir string
	tokens  []conditionToken
	pos     int
}

func (p *conditionParser) peek() (conditionToken, bool) {
	if p.pos >= len(p.tokens) {
		return conditionToken{}, false
	}
	return p.tokens[p.pos], true
}

func (p *conditionParser) keyword(word string) bool {
	token, ok := p.peek()
	if ok && token.kind == 'w' && strings.EqualFold(token.text, word) {
		p.pos++
		return true
	}
	return false
}

func (p *conditionParser) parseOr() (truth, bool) {
	left, ok := p.parseAnd()
	for ok && p.keyword("or") {
		var right truth
		right, ok = p.parseAnd()
		left = left.or(right)
	}
	return left, ok
}

func (p *conditionParser) parseAnd() (truth, bool) {
	left, ok := p.parseUnary()
	for ok && p.keyword("and") {
		var right truth
		right, ok = p.parseUnary()
		left = left.and(right)
	}
	return left, ok
}

func (p *conditionParser) parseUnary() (truth, bool) {
	if token, ok := p.peek(); ok && token.kind == 'o' && token.text == "!" {
		p.pos++
		result, ok := p.parseUnary()
		return result.not(), ok
	}
	return p.parseComparison()
}

func (p *conditionParser) parseComparison() (truth, bool) {
	token, ok := p.peek()
	if !ok {
		return unknown, false
	}
	if token.kind == '(' {
		p.pos++
		result, ok := p.parseOr()
		if closing, more := p.peek(); !ok || !more || closing.kind != ')' {
			return unknown, false
		}
		p.pos++
		return result, true
	}
	if token.kind == 'w' && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].kind == '(' {
		return p.parseFunction(token.text)
	}
	left, ok := p.parseOperand()
	if !ok {
		return unknown, false
	}
	operator, more := p.peek()
	if !more || operator.kind != 'o' || operator.text == "!" {
		return booleanValue(left), true
	}
	p.pos++
	right, ok := p.parseOperand()
	if !ok {
		return unknown, false
	}
	if !left.Known || !right.Known {
		return unknown, true
	}
	switch operator.text {
	case "==":
		return truthOf(strings.EqualFold(left.Text, right.Text)), true
	case "!=":
		return truthOf(!strings.EqualFold(left.Text, right.Text)), true
	}
	a, errA := strconv.ParseFloat(strings.TrimSpace(left.Text), 64)
	b, errB := strconv.ParseFloat(strings.TrimSpace(right.Text), 64)
	if errA != nil || errB != nil {
		return unknown, true // MSBuild also compares versions here; not modeled
	}
	switch operator.text {
	case "<":
		return truthOf(a < b), true
	case ">":
		return truthOf(a > b), true
	case "<=":
		return truthOf(a <= b), true
	case ">=":
		return truthOf(a >= b), true
	}
	return unknown, false
}

func (p *conditionParser) parseOperand() (value, bool) {
	token, ok := p.peek()
	if !ok || token.kind != 's' && token.kind != 'w' {
		return value{}, false
	}
	p.pos++
	return p.scope.expand(token.text), true
}

func (p *conditionParser) parseFunction(name string) (truth, bool) {
	p.pos += 2 // name and (
	var args []value
	for {
		token, ok := p.peek()
		if !ok {
			return unknown, false
		}
		if token.kind == ')' {
			p.pos++
			break
		}
		if token.kind == ',' {
			p.pos++
			continue
		}
		arg, ok := p.parseOperand()
		if !ok {
			return unknown, false
		}
		args = append(args, arg)
	}
	if len(args) != 1 {
		return unknown, true
	}
	if !args[0].Known {
		return unknown, true
	}
	switch strings.ToLower(name) {
	case "exists":
		path := strings.TrimSpace(args[0].Text)
		if path == "" {
			return isFalse, true
		}
		location := absoluteFrom(p.baseDir, path)
		_, err := os.Stat(location)
		if err != nil {
			p.scope.eval.absent(location)
		}
		return truthOf(err == nil), true
	case "hastrailingslash":
		text := args[0].Text
		return truthOf(strings.HasSuffix(text, "/") || strings.HasSuffix(text, `\`)), true
	}
	return unknown, true
}

func booleanValue(v value) truth {
	if !v.Known {
		return unknown
	}
	switch strings.ToLower(strings.TrimSpace(v.Text)) {
	case "true", "on", "yes", "!false", "!off", "!no":
		return isTrue
	case "false", "off", "no", "!true", "!on", "!yes":
		return isFalse
	}
	return unknown
}

func truthOf(b bool) truth {
	if b {
		return isTrue
	}
	return isFalse
}
