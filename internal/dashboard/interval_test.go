package dashboard

import (
	"netwatch/internal/classifier"
	"netwatch/internal/event"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestIntervalOverview(t *testing.T) {
	start := time.Unix(100, 0)
	d := newDashboard(GeoOptions{})
	d.overview = newOverview(start, start.Add(time.Minute))
	for i := 0; i < 2; i++ {
		d.add(event.AccessEvent{Time: start.Add(time.Duration(i) * 59 * time.Second), ClientIP: "8.8.8.8", Status: 200})
	}
	if d.total != 2 || len(d.clients) != 1 || d.overview.timeline[0] != 1 || d.overview.timeline[118] != 1 {
		t.Fatal("interval totals or bins")
	}
	ansi := regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]")
	for index := range themes {
		d.themeIndex = index
		rows := d.overviewRows(80, 24, 0, map[classifier.Category]int{classifier.User: 1})
		if len(rows) != 24 {
			t.Fatal(len(rows))
		}
		for _, line := range rows {
			if utf8.RuneCountInString(ansi.ReplaceAllString(line, "")) != 79 {
				t.Fatal("row width")
			}
		}
		if !strings.Contains(rows[23], themes[index].Name) {
			t.Fatal("theme missing")
		}
	}
}
