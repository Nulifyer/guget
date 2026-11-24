package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nulifyer/guget/internal/selfupdate"
)

// runSelfUpdate parses argv itself so tests can inject the updater.
func (r *cliRuntime) runSelfUpdate(ctx context.Context, argv []string, updater *selfupdate.Updater, executable func() (string, error)) ExitCode {
	spec := lookupCLIVerb("self-update")
	if wantsHelp(argv) {
		fmt.Fprint(r.stdout, verbHelp(spec))
		return ExitSuccess
	}
	inv, err := r.prepareInvocation(spec, argv)
	if err != nil {
		return r.usageError(spec, err)
	}
	return r.runSelfUpdateWith(ctx, inv, updater, executable)
}

func (r *cliRuntime) runSelfUpdateWith(ctx context.Context, inv *cliInvocation, updater *selfupdate.Updater, executable func() (string, error)) ExitCode {
	args := inv.args
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
