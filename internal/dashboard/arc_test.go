package dashboard

import (
	"strings"
	"testing"

	"netwatch/internal/event"
)

func TestTrafficPulseBudget(t *testing.T) {
	d := newDashboard(GeoOptions{})
	d.frame(160, 55)
	if d.signalCap != 12 {
		t.Fatalf("large panel budget = %d", d.signalCap)
	}
	for i := 0; i < 30; i++ {
		d.startSignal(geoKey{longitude: i * 100})
		d.add(event.AccessEvent{Status: 200})
	}
	if len(d.signals) != 12 || d.total != 30 {
		t.Fatalf("signals = %d, requests = %d", len(d.signals), d.total)
	}
	d.frame(80, 24)
	if d.signalCap != 3 || len(d.signals) != 3 {
		t.Fatalf("resized budget = %d, signals = %d", d.signalCap, len(d.signals))
	}
	if signalLimit(400, 15) > 3 {
		t.Fatal("empty columns counted as globe area")
	}
	for i := 0; i < signalFrames; i++ {
		d.advanceSignals()
	}
	d.startSignal(geoKey{})
	d.startSignal(geoKey{})
	if len(d.signals) != 1 || d.signals[0].Age != 0 {
		t.Fatal("expired signals must free slots and repeat requests must coalesce")
	}
}

func TestTrafficPulseFrames(t *testing.T) {
	server := geoMarker{Latitude: 50, Longitude: -30}
	render := func(s *geoMarker, visitors ...geoMarker) string {
		return strings.Join(renderGlobeWithTraffic(36, 15, 0, themes[4], s, visitors), "\n")
	}
	for _, destination := range []*geoMarker{nil, &server} {
		base := render(destination)
		marker := geoMarker{Latitude: 10, Longitude: 55}
		if render(destination, marker) == base {
			t.Fatal("expected a visible pulse at its origin")
		}
		marker.Age = 3
		if render(destination, marker) != base {
			t.Fatal("blink must restore the underlying frame")
		}
	}
	project := func(lon, lat float64) screenPoint { return screenPoint{x: int(lon + 100), depth: 1} }
	last := -1
	for _, age := range []int{0, 4, 8} {
		cells, head := 0, -1
		drawGeoArc(0, 0, 0, 60, age, project, func(x, y int, glyph rune) {
			cells++
			if glyph != '.' {
				head = x
			}
		})
		if cells > 3 || head <= last {
			t.Fatalf("age %d: cells = %d, head = %d", age, cells, head)
		}
		last = head
	}
}
