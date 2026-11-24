package main

import (
	"regexp"
	"strings"
)

// redactedSecret replaces credentials embedded in URL userinfo wherever Guget
// displays a source URL or a message that may quote one.
const redactedSecret = "***"

// urlUserinfoPattern finds "scheme://userinfo@" so free text such as error
// messages can be redacted without parsing every URL it may contain.
var urlUserinfoPattern = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.\-]*://)([^/?#\s"'<>]+)@`)

// redactURL hides URL userinfo credentials: https://user:pass@host becomes
// https://user:***@host. A userinfo without a password is redacted entirely,
// because feeds commonly accept an access token in the username position.
// The rest of the URL is returned unchanged.
func redactURL(raw string) string {
	return redactCredentials(raw)
}

// redactCredentials applies redactURL to every URL found in text.
func redactCredentials(text string) string {
	if !strings.Contains(text, "@") || !strings.Contains(text, "://") {
		return text
	}
	return urlUserinfoPattern.ReplaceAllStringFunc(text, func(match string) string {
		parts := urlUserinfoPattern.FindStringSubmatch(match)
		scheme, userinfo := parts[1], parts[2]
		user, _, hasPassword := strings.Cut(userinfo, ":")
		if hasPassword && user != "" {
			return scheme + user + ":" + redactedSecret + "@"
		}
		return scheme + redactedSecret + "@"
	})
}
