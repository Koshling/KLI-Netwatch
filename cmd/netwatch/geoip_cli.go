package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"netwatch/internal/geoip"
)

// runGeoIPCLI handles GeoIP maintenance before the dashboard or its
// configuration is initialized. It reports an exit code for main to use.
func runGeoIPCLI(args []string, stdout, stderr io.Writer) int {
	const usage = "Usage:\n  netwatch geoip convert -input SOURCE.csv -output DEST.nwgeo [-family 4|6]\n"

	if len(args) > 0 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		fmt.Fprint(stdout, usage)
		return 0
	}
	if len(args) == 0 || args[0] != "convert" {
		if len(args) > 0 {
			fmt.Fprintf(stderr, "unknown geoip command: %q\n", args[0])
		}
		fmt.Fprint(stderr, usage)
		return 2
	}

	fs := flag.NewFlagSet("netwatch geoip convert", flag.ContinueOnError)
	fs.SetOutput(stderr)
	input := fs.String("input", "", "source GeoIP CSV")
	output := fs.String("output", "", "destination NWGEO file (must not exist)")
	family := fs.Int("family", 4, "IP family: 4 or 6")
	fs.Usage = func() {
		fmt.Fprint(stderr, usage)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *input == "" || *output == "" || fs.NArg() != 0 {
		fs.Usage()
		return 2
	}
	if *family != 4 && *family != 6 {
		fmt.Fprintln(stderr, "family must be 4 or 6")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := geoip.ConvertCSVContext(ctx, *input, *output, *family); err != nil {
		fmt.Fprintln(stderr, "Conversion failed:", cleanDiagnostic(err.Error()))
		return 1
	}
	fmt.Fprintln(stdout, "Created NWGEO database:", cleanDiagnostic(*output))
	return 0
}
