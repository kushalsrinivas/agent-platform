// Package run coordinates service goroutines and signal-driven shutdown.
package run

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sync/errgroup"
)

// SignalContext is cancelled on SIGINT or SIGTERM.
func SignalContext() context.Context {
	ctx, _ := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	return ctx
}

// Group runs fns until one returns an error or ctx is cancelled. The returned error is the first non-nil error;
// context cancellation is reported as nil so Ctrl-C is a clean exit.
func Group(ctx context.Context, fns ...func(context.Context) error) error {
	group, groupCtx := errgroup.WithContext(ctx)
	for _, fn := range fns {
		group.Go(func() error { return fn(groupCtx) })
	}
	err := group.Wait()
	if err != nil && ctx.Err() != nil {
		return nil
	}
	return err
}
