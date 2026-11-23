package geoip

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestBadFirstRange(t *testing.T) {
	p := makeDB(t, "ranges.csv", "oops,2,CC,C,R,T,1,2\n3,4,CC,C,R,T,1,2\n")
	if _, err := Open(p, ""); err == nil {
		t.Error("CSV loader discarded invalid first row")
	}
	dest := filepath.Join(t.TempDir(), "out.nwgeo")
	if err := ConvertCSV(p, dest, 4); err == nil {
		t.Error("converter discarded invalid first row")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("unexpected output", err)
	}
}

func TestInvalidCoordinates(t *testing.T) {
	for _, coords := range []string{"NaN,0", "0,NaN"} {
		p := makeDB(t, "ranges.csv", "1,2,CC,C,R,T,"+coords+"\n")
		if _, err := Open(p, ""); err == nil {
			t.Errorf("CSV accepted %s", coords)
		}
	}
	r, path := binaryCSV(t, "134744064,134744319,CC,C,R,T,0,0\n", 4)
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var raw [8]byte
	binary.LittleEndian.PutUint64(raw[:], math.Float64bits(181))
	if _, err := f.WriteAt(raw[:], binaryHeaderSize+40); err != nil {
		t.Fatal(err)
	}
	if r.Lookup("8.8.8.8").Found {
		t.Error("binary lookup returned invalid longitude")
	}
}
