package dashboard

import (
	"crypto/sha256"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"netwatch/internal/classifier"
	"netwatch/internal/event"
)

var overviewSink *overview

func BenchmarkOverviewEmpty(b *testing.B) {
	for b.Loop() {
		overviewSink = newOverview(time.Unix(1, 0), time.Unix(2, 0))
	}
}
func BenchmarkFeedStream(b *testing.B) {
	d := newDashboard(GeoOptions{})
	e := event.AccessEvent{Time: time.Unix(10, 0), Method: "GET", URL: "/hello", Status: 200}
	for b.Loop() {
		d.add(e)
	}
}

func BenchmarkGlobeFrame(b *testing.B) {
	d := newDashboard(GeoOptions{ServerSet: true, ServerLatitude: 32, ServerLongitude: 11})
	d.currentSecond = time.Unix(20, 0)
	d.add(event.AccessEvent{Time: d.currentSecond, ClientIP: "8.8.8.8", Method: "GET",
		URL: "/" + strings.Repeat("индекс/", 100), Status: 200})
	for b.Loop() {
		d.angle += globeStep
		d.frame(120, 30)
	}
}

func TestGlobeFrameVisualRegression(t *testing.T) {
	hash := sha256.New()
	d := newDashboard(GeoOptions{ServerSet: true, ServerLatitude: 32, ServerLongitude: 11})
	d.currentSecond = time.Unix(20, 0)
	d.add(event.AccessEvent{Time: d.currentSecond, ClientIP: "8.8.8.8", Method: "GET",
		URL: "/" + strings.Repeat("индекс/", 100), Status: 200})
	d.signals = []geoMarker{{Latitude: 12, Longitude: 16, Age: 3, Seen: d.currentSecond, Category: classifier.Bot}}
	for theme := range themes {
		d.themeIndex = theme
		for _, size := range [][2]int{{60, 18}, {80, 24}, {120, 30}, {160, 48}} {
			for i := range 8 {
				d.angle = float64(i) * math.Pi / 4
				frame, _ := d.frame(size[0], size[1])
				fmt.Fprintln(hash, strings.Join(frame, "\n"))
			}
		}
	}
	if got, want := fmt.Sprintf("%x", hash.Sum(nil)), "90269e45e87cedbc5c54ba073fb52839f63ea83f073930f499b0a47b826c240f"; got != want {
		t.Fatalf("frame digest = %s, want %s", got, want)
	}
}
