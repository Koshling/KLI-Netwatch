package dashboard

import (
	"strings"
	"testing"

	"netwatch/internal/classifier"
)

func TestWhiteHeadings(t *testing.T) {
	for _, index := range []int{1, 5} {
		theme := themes[index]
		d := Dashboard{themeIndex: index, hideGeoOverlay: true}
		frame, _ := d.frame(132, 40)
		for _, heading := range []string{"REQ ", "GLOBAL TRAFFIC", "LIVE EVENT FEED", "REQUEST RATE / SEC", "HTTP STATUS", "CLIENTS BY TYPE"} {
			if !strings.Contains(strings.Join(frame, "\n"), "\033[1;97m"+heading) {
				t.Errorf("%s missing white heading %q", theme.Name, heading)
			}
		}
		for _, columns := range []int{0, 2} {
			menu := strings.Join(menuRows(132, 40, columns, 0, 0, "", theme), "\n")
			if !strings.Contains(menu, "\033[1;97mMENU") || !strings.Contains(menu, "\033[1;97m  REPLAY") {
				t.Errorf("%s menu lacks white labels in %d columns", theme.Name, columns)
			}
		}
	}
}

func TestIceAndAmberGlobeOverlay(t *testing.T) {
	for _, theme := range themes[5:] {
		overlay := []globeCell{
			{glyph: '@', category: classifier.User},
			{glyph: '@', category: classifier.Bot},
			{glyph: '@', category: classifier.Threat},
		}
		got := applyGlobeOverlay(theme.Accent+"###\033[0m", overlay, theme)
		seen := map[string]bool{theme.Accent: true, theme.Data: true}
		for _, cell := range overlay {
			color := pulseColor(cell.category, theme)
			if seen[color] || !strings.Contains(got, color+"@") {
				t.Errorf("%s %s marker does not stand out: %q", theme.Name, cell.category, got)
			}
			seen[color] = true
		}
	}
}
