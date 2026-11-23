package geoip

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func BenchmarkDiskBlockLookup(b *testing.B) {
	var csv strings.Builder
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&csv, "%d,%d,US,Test,Region,City%d,1.25,2.5\n", 134217728+i*4, 134217729+i*4, i)
	}
	dir := b.TempDir()
	src := filepath.Join(dir, "in.csv")
	dst := filepath.Join(dir, "out.db")
	if err := os.WriteFile(src, []byte(csv.String()), 0600); err != nil {
		b.Fatal(err)
	}
	if err := ConvertCSV(src, dst, 4); err != nil {
		b.Fatal(err)
	}
	r, err := OpenBinary(dst)
	if err != nil {
		b.Fatal(err)
	}
	defer r.Close()
	for b.Loop() {
		if v := r.Lookup("8.0.0.4"); v.City != "City1" {
			b.Fatal(v)
		}
	}
}
