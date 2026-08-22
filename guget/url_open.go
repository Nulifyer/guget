package main

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
	"strings"

	bubble_tea "charm.land/bubbletea/v2"
)

func openURLCmd(ctx context.Context, rawURL string) bubble_tea.Cmd {
	return func() bubble_tea.Msg {
		return openURLResultMsg{url: rawURL, err: openExternalURL(ctx, rawURL)}
	}
}

func openExternalURL(ctx context.Context, rawURL string) error {
	command, args, err := externalURLCommand(runtime.GOOS, rawURL)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, command, args...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("opening %s: %w", shortURLHost(rawURL), err)
	}
	return nil
}

func externalURLCommand(goos, rawURL string) (string, []string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", nil, fmt.Errorf("invalid URL: %w", err)
	}
	if parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", nil, fmt.Errorf("unsupported URL: %s", rawURL)
	}

	switch goos {
	case "darwin":
		return "open", []string{rawURL}, nil
	case "windows":
		return "rundll32.exe", []string{"url.dll,FileProtocolHandler", rawURL}, nil
	default:
		return "xdg-open", []string{rawURL}, nil
	}
}

func shortURLHost(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return strings.TrimSpace(rawURL)
	}
	return parsed.Host
}
