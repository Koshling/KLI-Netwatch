package dashboard

import (
	"fmt"
	"testing"
	"time"

	"netwatch/internal/classifier"
	"netwatch/internal/event"
)

func TestFeedRetainsLastHundredInOrder(t *testing.T) {
	d := newDashboard(GeoOptions{})
	var want []feedEvent
	for i := 0; i < 306; i++ {
		e := event.AccessEvent{Time: time.Unix(int64(i), 0), ClientIP: "8.8.8.8", URL: fmt.Sprintf("/%d", i), Method: "GET", Status: 200}
		category := classifier.User
		if i >= 170 {
			e.UserAgent = "Googlebot"
			category = classifier.Bot
		}
		d.add(e)
		want = append(want, feedEvent{when: e.Time, clientIP: e.ClientIP, url: e.URL, method: e.Method, status: e.Status, category: category})
		if len(want) > 100 {
			want = want[1:]
		}
		if len(d.feed) != len(want) {
			t.Fatalf("request %d: retained %d entries, want %d", i, len(d.feed), len(want))
		}
		for j, item := range want {
			if d.feedAt(j) != item {
				t.Fatalf("request %d: mismatch at entry %d", i, j)
			}
		}
	}
}
