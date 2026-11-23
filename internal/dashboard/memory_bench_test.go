package dashboard

import (
	"testing"
	"time"

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
