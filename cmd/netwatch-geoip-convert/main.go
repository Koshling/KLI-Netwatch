package main

import (
	"flag"
	"fmt"
	"netwatch/internal/geoip"
	"os"
)

func main() {
	input := flag.String("input", "", "source GeoIP CSV")
	output := flag.String("output", "", "destination NWGEO binary file (must not exist)")
	family := flag.Int("family", 4, "IP family: 4 or 6")
	flag.Parse()
	if *input == "" || *output == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := geoip.ConvertCSV(*input, *output, *family); err != nil {
		fmt.Fprintln(os.Stderr, "Conversion failed:", err)
		os.Exit(1)
	}
	fmt.Println("Created NWGEO database:", *output)
}
