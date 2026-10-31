package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nulifyer/guget/internal/selfupdate"
)

func (r cliRuntime) runSelfUpdate(ctx context.Context, argv []string, updater *selfupdate.Updater, executable func() (string, error)) ExitCode {
	args, err := parseCLIArgs(argv, nil, map[string]bool{"check": true, "help": true})
	if err != nil || len(args.positionals) != 0 {
		if err != nil {
			fmt.Fprintln(r.stderr, err)
		}
		fmt.Fprint(r.stderr, commandUsage("self-update"))
		return ExitUsage
	}
	if args.bools["help"] {
		fmt.Fprint(r.stdout, commandUsage("self-update"))
		return ExitSuccess
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	release, err := updater.Latest(ctx)
	if err != nil {
		fmt.Fprintln(r.stderr, err)
		if errors.Is(err, selfupdate.ErrDevelopment) {
			return ExitRefused
		}
		return ExitSource
	}
	if !release.Available {
		fmt.Fprintln(r.stdout, "Guget is already current; no newer stable release is available.")
		return ExitSuccess
	}
	if args.bools["check"] {
		fmt.Fprintf(r.stdout, "guget %s available; run guget self-update to install.\n", release.Version)
		return ExitSuccess
	}
	path, err := executable()
	if err == nil {
		err = updater.Install(ctx, release, path)
	}
	if err != nil {
		fmt.Fprintf(r.stderr, "Self-update failed: %v\n", err)
		return ExitWrite
	}
	fmt.Fprintf(r.stdout, "Updated guget to %s.\n", release.Version)
	return ExitSuccess
}
