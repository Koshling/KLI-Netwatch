package dashboard

import (
	"strings"
	"strconv"
	"time"
	"netwatch/internal/classifier"
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
		d.startSignal(geoKey{longitude: i * 100}, strconv.Itoa(i), classifier.User, time.Time{})
		d.add(event.AccessEvent{Status: 200})
	}
	d.frame(160, 55)
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
	d.startSignal(geoKey{}, "0", classifier.User, time.Time{})
	d.startSignal(geoKey{}, "0", classifier.User, time.Time{})
	d.scheduleSignals()
	if len(d.signals) != 3 || d.signals[0].Age != 0 {
		t.Fatal("expired signals must free slots and repeat requests must coalesce")
	}
}

func TestTrafficPulseFrames(t *testing.T) {
	server := geoMarker{Latitude: 50, Longitude: -30}
	render := func(s *geoMarker, visitors ...geoMarker) string {
		return strings.Join(renderGlobeWithTraffic(36, 15, 0, themes[4], s, visitors, time.Time{}), "\n")
	}
	for _, destination := range []*geoMarker{nil, &server} {
		base := render(destination)
		marker := geoMarker{Latitude: 10, Longitude: 55}
		if render(destination, marker) == base {
			t.Fatal("expected a visible pulse at its origin")
		}
		marker.Age = 3
		if !strings.Contains(render(destination, marker), "*") {
			t.Fatal("pulse head disappeared")
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

func TestSourceAgeAndQueuedCategories(t *testing.T) {
	for second, want := range map[int]rune{0:'o', 5:'@', 15:':', 30:0} {
		if got := sourceGlyph(time.Duration(second)*time.Second); got != want {
			t.Fatalf("age %d: got %q, want %q", second, got, want)
		}
	}
	d := newDashboard(GeoOptions{})
	d.signalCap = 1
	now := time.Unix(100, 0)
	d.currentSecond = now
	d.startSignal(geoKey{}, "one", classifier.User, now)
	d.startSignal(geoKey{}, "two", classifier.Bot, now)
	d.scheduleSignals()
	if len(d.signals) != 1 || d.signals[0].Category != classifier.User || len(d.pending) != 1 {
		t.Fatal("two clients at the same coordinates must each get a turn")
	}
	for i := 0; i < signalFrames; i++ { d.advanceSignals() }
	d.scheduleSignals()
	if len(d.signals) != 1 || d.signals[0].Category != classifier.Bot {
		t.Fatal("queued pulse lost its classification")
	}
}

func TestSignalWaitsForVisibleLaunch(t *testing.T) {
	d := newDashboard(GeoOptions{})
	d.signalCap = 1
	d.startSignal(geoKey{longitude: 8700}, "edge", classifier.Bot, time.Time{})
	d.scheduleSignals()
	if len(d.signals) != 0 || len(d.pending) != 1 {
		t.Fatal("source at the rim must keep its turn")
	}
	d.angle = -1.5
	d.scheduleSignals()
	if len(d.signals) != 1 || len(d.pending) != 0 {
		t.Fatal("source should launch after rotating into view")
	}
}
