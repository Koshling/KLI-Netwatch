package dashboard

import (
	"testing"
	"time"
)

func TestLiveRateAfterGap(t *testing.T) {
	for _, gap := range []int{1, 3, 120, 121} {
		d := newDashboard(GeoOptions{})
		start := time.Unix(1000, 0)
		d.updateRate(start)
		d.currentCount = 7
		d.updateRate(start.Add(time.Duration(gap) * time.Second))
		for i, n := range d.rates {
			want := 0
			if i == len(d.rates)-gap {
				want = 7
			}
			if n != want {
				t.Errorf("gap %d slot %d: got %d want %d", gap, i, n, want)
			}
		}
	}
}
