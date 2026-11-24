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

	"netwatch/internal/dashboard"
	"netwatch/internal/geoip"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "geoip" {
		os.Exit(runGeoIPCLI(os.Args[2:], os.Stdout, os.Stderr))
	}

	if len(os.Args) > 1 && os.Args[1] == "setup" {
		fs := flag.NewFlagSet("netwatch setup", flag.ExitOnError)
		path := fs.String("config", "", "configuration file (defaults to automatic discovery)")
		fs.Parse(os.Args[2:])
		if fs.NArg() != 0 {
			fs.Usage()
			os.Exit(2)
		}
		if _, err := runSetup(*path, true, false, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "Setup error:", err)
			os.Exit(1)
		}
		return
	}

	configPath := flag.String("config", "", "configuration file (defaults to automatic discovery)")
	autoConfig := flag.Bool("auto-config", false, "create configuration from local files and exit")
	mode := flag.String("mode", "live", "live, replay or interval")
	logPath := flag.String("log", "", "historical log (defaults to access_log)")
	last := flag.Duration("last", dashboard.DefaultHistoryDuration, "replay duration when explicit bounds are absent")
	fromFlag := flag.String("from", "", "replay start (RFC3339)")
	toFlag := flag.String("to", "", "replay end (RFC3339, exclusive)")
	interval := flag.String("interval", "", "overview: duration or RFC3339-from/RFC3339-to")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "unexpected argument: %q\n", flag.Arg(0))
		os.Exit(2)
	}
	if *autoConfig {
		flag.Visit(func(f *flag.Flag) {
			if f.Name != "auto-config" && f.Name != "config" {
				fmt.Fprintf(os.Stderr, "--auto-config conflicts with --%s\n", f.Name)
				os.Exit(2)
			}
		})
		if err := runAutoConfig(*configPath, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "Setup error:", err)
			os.Exit(1)
		}
		return
	}
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
		fmt.Fprintln(os.Stderr, "historical options require --mode replay or interval")
		os.Exit(2)
	}

	cfg, err := runSetup(*configPath, false, terminal(os.Stdin) && terminal(os.Stdout), os.Stdin, os.Stderr)
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
			fmt.Fprintf(os.Stderr, "GeoIP disabled: %v\n", err)
			resolver = nil
		}
	}

	if resolver != nil {
		if cfg.GeoIPSource == "db-ip" {
			fmt.Fprintln(os.Stderr, geoip.DBIPAttribution)
		}
		defer resolver.Close()
	}

	geoOptions := dashboard.GeoOptions{
		Resolver:        resolver,
		ServerLatitude:  cfg.ServerLatitude,
		ServerLongitude: cfg.ServerLongitude,
		ServerSet:       cfg.ServerCoordinatesSet,
	}

	launch := dashboard.LaunchOptions{
		Mode: dashboard.Mode(*mode), LogPath: *logPath, Last: *last,
		From: *fromFlag, To: *toFlag,
	}
	if launch.Mode != dashboard.Live {
		if explicitLast && (launch.From != "" || launch.To != "") {
			fmt.Fprintln(os.Stderr, "supply both --from and --to in RFC3339 without --last")
			os.Exit(2)
		}
		if _, _, err := launch.HistoryBounds(time.Now()); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}

	if err := runMonitor(ctx, cfg, geoOptions, launch); err != nil {
		fmt.Fprintf(os.Stderr, "Dashboard error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("\nNETWATCH stopped.")
}
