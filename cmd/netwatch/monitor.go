package main

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"netwatch/internal/config"
	"netwatch/internal/dashboard"
	"netwatch/internal/event"
	"netwatch/internal/history"
	"netwatch/internal/reader"
	"netwatch/internal/regularfile"
)

func runMonitor(ctx context.Context, cfg config.Config, geo dashboard.GeoOptions, launch dashboard.LaunchOptions) error {
	parse, err := cfg.AccessParser()
	if err != nil {
		return err
	}
	keep, err := cfg.TrafficFilter()
	if err != nil {
		return err
	}
	session := &dashboard.Session{}
	defer session.Close()

	// A direct CLI launch reports failures to stderr. Once the user opens the
	// menu, a failed launch returns there so the settings can be corrected.
	err = runMode(ctx, session, cfg, parse, keep, geo, launch)
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
		err = runMode(ctx, session, cfg, parse, keep, geo, launch)
	}
	return nil
}

func runMode(ctx context.Context, session *dashboard.Session, cfg config.Config, parse event.AccessParser, keep event.TrafficFilter, geo dashboard.GeoOptions, launch dashboard.LaunchOptions) error {
	if launch.Mode == dashboard.Live {
		modeCtx, cancel := context.WithCancel(ctx)
		// Bound the queue by both record size and the number of events.
		const queueBytes = 8 << 20
		events := make(chan event.AccessEvent, min(512, queueBytes/max(1, cfg.MaxLineSize)))
		var skipped atomic.Uint64
		readerDone := make(chan error, 1)
		go func() {
			defer close(events)
			readerDone <- followAccess(modeCtx, cfg, parse, keep, events, &skipped)
		}()
		err := session.RunLive(modeCtx, events, geo, &skipped)
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
	file, info, err := regularfile.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	scan := func(ctx context.Context, emit func(event.AccessEvent) error) (int, error) {
		return history.ScanSnapshot(ctx, file, info.Size(), parse, cfg.MaxLineSize, start, end, func(e event.AccessEvent) error {
			if !keep(e) {
				return nil
			}
			return emit(e)
		})
	}
	if launch.Mode == dashboard.Interval {
		return session.RunInterval(ctx, scan, geo, start, end)
	}
	return session.RunReplay(ctx, scan, geo, start, end)
}

func followAccess(ctx context.Context, cfg config.Config, parse event.AccessParser, keep event.TrafficFilter, events chan<- event.AccessEvent, skipped *atomic.Uint64) error {
	r := reader.New(cfg.AccessLog, cfg.MaxLineSize, cfg.PollInterval)
	r.OnOversizedLine = func() { skipped.Add(1) }
	return r.Follow(ctx, true, func(line string) error {
		ev, err := parse(line)
		if err != nil {
			skipped.Add(1)
			return nil
		}
		if !keep(ev) {
			return nil
		}
		// These fields are unused after filtering and can retain decoded data.
		ev.Referer, ev.XForwardedFor, ev.Protocol = "", "", ""
		select {
		case events <- ev:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
}
