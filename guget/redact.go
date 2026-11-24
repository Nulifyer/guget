package main

import (
	"net/url"
	"strings"
)

// redactedSecret replaces credentials embedded in URL userinfo wherever Guget
// displays a source URL or a message that may quote one.
const redactedSecret = "***"

// redactURL hides URL userinfo credentials: https://user:pass@host becomes
// https://user:***@host. A userinfo without a password is redacted entirely,
// because feeds commonly accept an access token in the username position.
// The rest of the URL is returned unchanged. A value net/url cannot parse is
// redacted as free text.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.User == nil || !strings.HasPrefix(raw[len(u.Scheme):], "://") {
		return redactCredentials(raw)
	}
	start := len(u.Scheme) + len("://")
	end := start + authorityLength(raw[start:], false)
	at := strings.LastIndexByte(raw[start:end], '@')
	if at < 0 {
		return redactCredentials(raw)
	}
	_, hasPassword := u.User.Password()
	return raw[:start] + redactUserinfo(raw[start:start+at], hasPassword && u.User.Username() != "") + raw[start+at:]
}

// redactCredentials redacts the userinfo of every URL found in free text,
// such as an error message. A URL's authority runs from "scheme://" to the
// first '/', '?', '#', or whitespace, and its userinfo is everything before
// the last '@' in it, so any character other than those may appear in a
// user name or password, quotes included.
func redactCredentials(text string) string {
	if !strings.Contains(text, "@") || !strings.Contains(text, "://") {
		return text
	}
	var b strings.Builder
	done := 0
	for search := 0; ; {
		found := strings.Index(text[search:], "://")
		if found < 0 {
			break
		}
		separator := search + found
		start := separator + len("://")
		search = start
		if !hasScheme(text[:separator]) {
			continue
		}
		end := start + authorityLength(text[start:], true)
		at := strings.LastIndexByte(text[start:end], '@')
		if at < 0 {
			search = end
			continue
		}
		userinfo := text[start : start+at]
		user, _, hasPassword := strings.Cut(userinfo, ":")
		b.WriteString(text[done:start])
		b.WriteString(redactUserinfo(userinfo, hasPassword && user != ""))
		done = start + at
		search = end
	}
	b.WriteString(text[done:])
	return b.String()
}

// authorityLength returns the length of the URL authority at the start of s:
// up to the first '/', '?', or '#', and in free text also whitespace.
func authorityLength(s string, freeText bool) int {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '/', '?', '#':
			return i
		case ' ', '\t', '\n', '\r', '\f', '\v':
			if freeText {
				return i
			}
		}
	}
	return len(s)
}

// hasScheme reports whether before ends with a URL scheme: a letter
// followed by letters, digits, '+', '-', or '.'.
func hasScheme(before string) bool {
	i := len(before)
	for i > 0 && isSchemeByte(before[i-1]) {
		i--
	}
	for ; i < len(before); i++ {
		if c := before[i]; c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' {
			return true
		}
	}
	return false
}

func isSchemeByte(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'
}

// redactUserinfo keeps the user name of a user:password pair and hides the
// password; any other userinfo, a token in the user name position, is hidden
// entirely.
func redactUserinfo(userinfo string, keepUser bool) string {
	if keepUser {
		user, _, _ := strings.Cut(userinfo, ":")
		return user + ":" + redactedSecret
	}
	return redactedSecret
}
