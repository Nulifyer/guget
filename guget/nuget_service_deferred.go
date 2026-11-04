package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Workspace discovery publishes local declarations before contacting feeds.
// Only operations that need a particular source initialize it. The resolved
// service is published as a separate object so rendering never races endpoint
// discovery or waits for network I/O.
type deferredNugetService struct {
	mu         sync.Mutex
	source     NugetSource
	configured []NugetSource
	attempt    *nugetServiceAttempt
}

type nugetServiceAttempt struct {
	done     chan struct{}
	ctx      context.Context
	service  *NugetService
	err      error
	canceled bool
}

func newDeferredNugetService(source NugetSource, configured []NugetSource) (*NugetService, error) {
	if !strings.HasPrefix(strings.ToLower(source.URL), "http://") && !strings.HasPrefix(strings.ToLower(source.URL), "https://") {
		return nil, fmt.Errorf("source is restore-only; metadata browsing requires an HTTP(S) NuGet V3 service index")
	}
	return &NugetService{
		sourceURL: source.URL, sourceName: source.Name,
		deferred: &deferredNugetService{source: source, configured: configured},
	}, nil
}

func (d *deferredNugetService) resolve(ctx context.Context) (*NugetService, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		d.mu.Lock()
		attempt := d.attempt
		if attempt != nil && attempt.ctx.Err() != nil {
			select {
			case <-attempt.done:
				if attempt.canceled {
					attempt = nil
				}
			default:
				attempt = nil
			}
		}
		if attempt == nil {
			attempt = &nugetServiceAttempt{done: make(chan struct{}), ctx: ctx}
			d.attempt = attempt
			go d.initialize(ctx, attempt)
		}
		d.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-attempt.done:
			// A search overlay may have started discovery before metadata work
			// joined it. Closing that overlay must not cancel the other caller.
			if attempt.canceled {
				continue
			}
			return attempt.service, attempt.err
		}
	}
}

func (d *deferredNugetService) initialize(ctx context.Context, attempt *nugetServiceAttempt) {
	service, err := NewNugetServiceContext(ctx, d.source)
	if err == nil {
		// Remove configured upstreams before publishing the immutable endpoints.
		// Deferred handles themselves have no endpoint data to deduplicate.
		services := []*NugetService{service}
		for _, source := range d.configured {
			services = append(services, &NugetService{sourceURL: source.URL})
		}
		DeduplicateADOUpstreams(services)
	}
	d.mu.Lock()
	attempt.service, attempt.err = service, err
	attempt.canceled = err != nil && ctx.Err() != nil
	// Reload cancels old metadata work but may reuse its feed handles. A
	// canceled initialization must not poison the next generation. Actual
	// source failures stay cached until manual/configuration reload replaces
	// the handles, avoiding one failed index request per installed package.
	if attempt.canceled && d.attempt == attempt {
		d.attempt = nil
	}
	close(attempt.done)
	d.mu.Unlock()
	if err != nil && ctx.Err() == nil {
		logWarn("Failed to initialise NuGet source [%s]: %v", d.source.Name, err)
	}
}

func (d *deferredNugetService) ready() *NugetService {
	d.mu.Lock()
	attempt := d.attempt
	d.mu.Unlock()
	if attempt != nil {
		select {
		case <-attempt.done:
			return attempt.service
		default:
		}
	}
	return nil
}
