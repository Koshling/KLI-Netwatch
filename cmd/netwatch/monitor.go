package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"netwatch/internal/config"
	"netwatch/internal/dashboard"
	"netwatch/internal/event"
	"netwatch/internal/history"
	"netwatch/internal/reader"
)

func runMonitor(ctx context.Context, cfg config.Config, geo dashboard.GeoOptions, launch dashboard.LaunchOptions) error {
	parse, err := cfg.AccessParser()
	if err != nil {
		return err
	}
	session := &dashboard.Session{}
	defer session.Close()

	// A direct CLI launch reports failures to stderr. Once the user opens the
	// menu, a failed launch returns there so the settings can be corrected.
	err = runMode(ctx, session, cfg, parse, geo, launch)
	if ctx.Err() != nil {
		return nil
	}
	if !errors.Is(err, dashboard.ErrMenu) {
		return err
	}
	for err != nil && ctx.Err() == nil {
		var problem error
		if !errors.Is(err, dashboard.ErrMenu) {
			problem = err
		}
		var selected bool
		launch, selected, err = session.Menu(ctx, launch, cfg.AccessLog, problem)
		if err != nil || !selected {
			return err
		}
		err = runMode(ctx, session, cfg, parse, geo, launch)
	}
	return nil
}

func runMode(ctx context.Context, session *dashboard.Session, cfg config.Config, parse event.AccessParser, geo dashboard.GeoOptions, launch dashboard.LaunchOptions) error {
	if launch.Mode == dashboard.Live {
		modeCtx, cancel := context.WithCancel(ctx)
		events := make(chan event.AccessEvent, 512)
		readerDone := make(chan error, 1)
		go func() {
			defer close(events)
			readerDone <- followAccess(modeCtx, cfg, parse, events)
		}()
		err := session.RunLive(modeCtx, events, geo)
		cancel()
		if readerErr := <-readerDone; err == nil && !errors.Is(readerErr, context.Canceled) {
			err = readerErr
		}
		return err
	}

	start, end, err := launch.HistoryBounds(time.Now())
	if err != nil {
		return err
	}
	path := launch.LogPath
	if path == "" {
		path = cfg.AccessLog
	}
	// A FIFO typed into the menu must not block before the regular-file check.
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("cannot read regular replay log: %s", path)
	}
	scan := func(ctx context.Context, emit func(event.AccessEvent) error) (int, error) {
		var previous time.Time
		return history.ScanSnapshot(ctx, file, info.Size(), parse, cfg.MaxLineSize, start, end, func(e event.AccessEvent) error {
			if e.Time.Before(previous) {
				return fmt.Errorf("timestamps out of order at %s", e.Time.Format(time.RFC3339Nano))
			}
			previous = e.Time
			return emit(e)
		})
	}
	if launch.Mode == dashboard.Interval {
		return session.RunInterval(ctx, scan, geo, start, end)
	}
	return session.RunReplay(ctx, scan, geo, start, end)
}

func followAccess(ctx context.Context, cfg config.Config, parse event.AccessParser, events chan<- event.AccessEvent) error {
	r := reader.New(cfg.AccessLog, cfg.MaxLineSize, cfg.PollInterval)
	return r.Follow(ctx, true, func(line string) error {
		ev, err := parse(line)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Access parse error: %v\n", err)
			return nil
		}
		select {
		case events <- ev:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
}
