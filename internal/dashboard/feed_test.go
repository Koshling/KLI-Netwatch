package dashboard

import (
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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

func TestClientLimitKeepsRequestTotalsAndTrackedCategories(t *testing.T) {
	d := newDashboard(GeoOptions{})
	now := time.Now()
	for i := range classifier.MaxClients + 8 {
		ip := netip.AddrFrom4([4]byte{8, byte(i >> 16), byte(i >> 8), byte(i)})
		d.add(event.AccessEvent{ClientIP: ip.String(), Time: now, Method: "GET", URL: "/", Status: 200})
	}
	d.add(event.AccessEvent{ClientIP: "8.0.0.0", Time: now, URL: "/", UserAgent: "Googlebot", Status: 200})
	if d.total != classifier.MaxClients+9 || d.statusCounts[0] != d.total ||
		len(d.clients) != classifier.MaxClients || !d.clientsLimited || d.bots != 1 {
		t.Fatalf("requests=%d clients=%s bots=%d", d.total, d.clientsLabel(), d.bots)
	}
	rows, _ := d.frame(120, 30)
	if !strings.Contains(rows[0], "CLIENTS 65536+") || !strings.Contains(strings.Join(rows, "\n"), "TRACKED CLIENT TYPES") {
		t.Fatal("partial client stats were not displayed")
	}
}

func TestFeedRetainsBoundedUTF8(t *testing.T) {
	d := newDashboard(GeoOptions{})
	long := strings.Repeat("界", 16000)
	d.add(event.AccessEvent{Method: long, URL: long})
	item := d.feedAt(0)
	for _, text := range []string{item.method, item.url} {
		if !utf8.ValidString(text) || len(text) > 2048 || !strings.HasSuffix(text, "…") {
			t.Fatal("invalid retained display field")
		}
	}
}
