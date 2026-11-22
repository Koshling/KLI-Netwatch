package dashboard

import (
	"regexp"
	"strings"
	"testing"
)

func TestServerVisitorArcVisible80x24(t *testing.T) {
	layout := calculateLayout(80, 24)
	visitors := []geoMarker{{Latitude: 10, Longitude: 55, Count: 1}}
	server := geoMarker{Latitude: 50, Longitude: -30}
	strip := regexp.MustCompile("\x1b\\[[0-9;]*m")
	render := func(s *geoMarker) []rune {
		lines := renderGlobeWithTraffic(layout.GlobeWidth, layout.GlobeHeight, 0, themes[4], s, visitors)
		return []rune(strip.ReplaceAllString(strings.Join(lines, "\n"), ""))
	}
	before, after := render(nil), render(&server)
	dots := 0
	for i, r := range after {
		if r == '.' && before[i] != r {
			dots++
		}
	}
	if dots < 2 || !strings.Contains(string(after), "S") {
		t.Fatalf("visible arc has %d new dots", dots)
	}
}
