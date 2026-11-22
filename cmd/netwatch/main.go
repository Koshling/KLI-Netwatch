package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"netwatch/internal/config"
	"netwatch/internal/dashboard"
	"netwatch/internal/event"
	"netwatch/internal/geoip"
	"netwatch/internal/history"
	"netwatch/internal/reader"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "geoip" {
		os.Exit(runGeoIPCLI(os.Args[2:], os.Stdout, os.Stderr))
	}

	configPath := flag.String("config", "configs/netwatch.json", "configuration file")
	mode := flag.String("mode", "live", "live, replay or interval")
	logPath := flag.String("log", "", "historical log (defaults to access_log)")
	last := flag.Duration("last", 15*time.Minute, "replay duration when explicit bounds are absent")
	fromFlag := flag.String("from", "", "replay start (RFC3339)")
	toFlag := flag.String("to", "", "replay end (RFC3339, exclusive)")
	interval := flag.String("interval", "", "overview: duration or RFC3339-from/RFC3339-to")
	flag.Parse()
	if *interval != "" || *mode == "interval" {
		if *mode != "live" && *mode != "interval" {
			fmt.Fprintln(os.Stderr, "--interval conflicts with --mode", *mode)
			os.Exit(2)
		}
		*mode = "interval"
		if *interval != "" {
			conflict := false
			flag.Visit(func(f *flag.Flag) {
				if f.Name == "last" || f.Name == "from" || f.Name == "to" {
					conflict = true
				}
			})
			if conflict {
				fmt.Fprintln(os.Stderr, "choose --interval or --last/--from/--to")
				os.Exit(2)
			}
			if a, b, ok := strings.Cut(*interval, "/"); ok {
				if a == "" || b == "" {
					fmt.Fprintln(os.Stderr, "expected start/end")
					os.Exit(2)
				}
				*fromFlag, *toFlag = a, b
			} else {
				dur, err := time.ParseDuration(*interval)
				if err != nil || dur <= 0 {
					fmt.Fprintln(os.Stderr, "expected positive duration or RFC3339 start/end")
					os.Exit(2)
				}
				*last = dur
			}
		}
	}
	if *mode != "live" && *mode != "replay" && *mode != "interval" {
		fmt.Fprintln(os.Stderr, "mode must be live, replay or interval")
		os.Exit(2)
	}
	var explicitLast bool
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "last" {
			explicitLast = true
		}
	})
	if *mode == "live" && (*logPath != "" || explicitLast || *fromFlag != "" || *toFlag != "") {
		fmt.Fprintln(os.Stderr, "historical options require --mode replay")
		os.Exit(2)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	var resolver *geoip.Resolver
	if cfg.GeoIPIPv4DB != "" || cfg.GeoIPIPv6DB != "" {
		resolver, err = geoip.Open(cfg.GeoIPIPv4DB, cfg.GeoIPIPv6DB)
		if err != nil {
			fmt.Fprintf(os.Stderr, "GeoIP disabled: %v\\n", err)
			resolver = nil
		}
	}

	if resolver != nil {
		defer resolver.Close()
	}

	geoOptions := dashboard.GeoOptions{
		Resolver:        resolver,
		ServerLatitude:  cfg.ServerLatitude,
		ServerLongitude: cfg.ServerLongitude,
		ServerSet:       cfg.ServerCoordinatesSet,
	}

	var runErr error
	if *mode != "live" {
		end := time.Now()
		start := end.Add(-*last)
		if *fromFlag != "" || *toFlag != "" {
			var fromErr, toErr error
			start, fromErr = time.Parse(time.RFC3339, *fromFlag)
			end, toErr = time.Parse(time.RFC3339, *toFlag)
			if explicitLast || fromErr != nil || toErr != nil {
				fmt.Fprintln(os.Stderr, "supply both --from and --to in RFC3339 without --last")
				os.Exit(2)
			}
		}
		if !start.Before(end) {
			fmt.Fprintln(os.Stderr, "replay interval must have positive duration")
			os.Exit(2)
		}
		path := *logPath
		if path == "" {
			path = cfg.AccessLog
		}
		file, err := os.Open(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			fmt.Fprintln(os.Stderr, "cannot read regular replay log:", path, err)
			os.Exit(1)
		}
		scan := func(ctx context.Context, emit func(event.AccessEvent) error) (int, error) {
			var previous time.Time
			return history.ScanSnapshot(ctx, file, info.Size(), cfg.AccessFormat, cfg.MaxLineSize, start, end, func(e event.AccessEvent) error {
				if e.Time.Before(previous) {
					return fmt.Errorf("timestamps out of order at %s", e.Time.Format(time.RFC3339Nano))
				}
				previous = e.Time
				return emit(e)
			})
		}
		if *mode == "interval" {
			runErr = dashboard.RunInterval(ctx, scan, geoOptions, start, end)
		} else {
			runErr = dashboard.RunReplay(ctx, scan, geoOptions, start, end)
		}
	} else {
		events := make(chan event.AccessEvent, 512)
		readerDone := make(chan error, 1)
		go func() {
			defer close(events)
			readerDone <- followAccess(ctx, cfg, events)
		}()
		runErr = dashboard.RunWithGeo(ctx, events, geoOptions)
		stop()
		if err := <-readerDone; runErr == nil && err != context.Canceled {
			runErr = err
		}
	}
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "Dashboard error: %v\n", runErr)
		stop()
		os.Exit(1)
	}

	fmt.Println("\nNETWATCH stopped.")
}

func followAccess(
	ctx context.Context,
	cfg config.Config,
	events chan<- event.AccessEvent,
) error {
	r := reader.New(cfg.AccessLog, cfg.MaxLineSize, cfg.PollInterval)

	return r.Follow(ctx, true, func(line string) error {
		ev, err := event.ParseAccess(line, cfg.AccessFormat)
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
