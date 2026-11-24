package dashboard

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"netwatch/internal/event"
)

func TestTerminalNavigationKeys(t *testing.T) {
	input, keys, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer keys.Close()
	if _, err := keys.WriteString("\x1b[A\x1bOB\x1b[1;2C\x1b[D[]mMgG\r\n\x1bOMф\x1b"); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(input)
	want := []rune{keyUp, keyDown, keyRight, keyLeft, '[', ']', 'm', 'M', 'g', 'G', '\r', '\n', '\r', 'ф', keyEscape}
	for i, expected := range want {
		key, err := readKey(reader, input)
		if key != expected || err != nil {
			t.Fatalf("key %d: got %q (%v), want %q", i, key, err, expected)
		}
	}
}

func TestMenuPrefersTwoByTwo(t *testing.T) {
	for _, tc := range []struct{ width, height, columns int }{
		{26, 11, 2}, {30, 12, 2}, {38, 13, 2}, {80, 20, 2}, {160, 24, 2}, {220, 55, 2},
		{160, 10, 4}, {25, 30, 1}, {18, 23, 1},
		{40, 10, 0}, {160, 7, 0}, {17, 40, 0},
	} {
		if got := menuColumns(tc.width, tc.height); got != tc.columns {
			t.Errorf("%dx%d: got %d columns, want %d", tc.width, tc.height, got, tc.columns)
		}
	}
}

func TestMenuAtCompactAndCardSizes(t *testing.T) {
	ansi := regexp.MustCompile("\x1b\\[[0-9;]*m")
	for _, size := range [][2]int{{12, 1}, {20, 3}, {36, 10}, {38, 13}, {76, 8}, {100, 28}, {160, 44}, {61, 30}, {26, 11}, {26, 30}, {34, 20}, {35, 20}, {18, 23}} {
		w, h := size[0], size[1]
		columns := menuColumns(w, h)
		theme := themes[1]
		for selected, card := range modeCards {
			rows := menuRows(w, h, columns, selected, 5, "", theme)
			if len(rows) != h {
				t.Fatalf("%dx%d: got %d rows", w, h, len(rows))
			}
			for _, row := range rows {
				cells := utf8.RuneCountInString(ansi.ReplaceAllString(row, ""))
				if cells > w || (columns > 0 && cells != w && row != "") {
					t.Fatalf("%dx%d: row overflow %q", w, h, row)
				}
			}
			text := strings.Join(rows, "\n")
			label := "> " + card.title
			if columns == 2 && w <= 34 && card.mode == convert {
				label = "> GEOIP"
			}
			if !strings.Contains(ansi.ReplaceAllString(text, ""), label[:min(len(label), w)]) {
				t.Fatalf("%dx%d: selection %s is hidden", w, h, card.title)
			}
			if columns > 0 && !strings.Contains(text, theme.Accent+"┌") {
				t.Fatalf("%dx%d: selected card border is missing the theme accent", w, h)
			}
		}
	}
	if moveModeSelection(0, 2, keyDown) != 2 || moveModeSelection(0, 4, keyLeft) != 3 ||
		moveModeSelection(3, 1, keyDown) != 0 {
		t.Fatal("selection did not follow the card layout")
	}
}

func TestHistoryBoundsForMenuAndCLI(t *testing.T) {
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	from := now.Add(-DefaultHistoryDuration)
	for _, test := range []struct {
		options LaunchOptions
		valid   bool
	}{
		{LaunchOptions{Last: DefaultHistoryDuration}, true},
		{LaunchOptions{From: from.Format(time.RFC3339), To: now.Format(time.RFC3339)}, true},
		{LaunchOptions{From: from.Format(time.RFC3339)}, false},
		{LaunchOptions{From: now.Format(time.RFC3339), To: from.Format(time.RFC3339)}, false},
		{LaunchOptions{Last: -time.Minute}, false},
	} {
		start, end, err := test.options.HistoryBounds(now)
		if test.valid && (err != nil || !start.Equal(from) || !end.Equal(now)) || !test.valid && err == nil {
			t.Errorf("options %+v: got %v to %v (%v)", test.options, start, end, err)
		}
	}
}

func TestGeoIPDestinationUsesFamilyInDirectories(t *testing.T) {
	dir := t.TempDir()
	for _, test := range []struct {
		path string
		want string
	}{
		{dir, filepath.Join(dir, "ipv6.nwgeo")},
		{dir + "/new/", filepath.Join(dir, "new", "ipv6.nwgeo")},
		{dir + "/custom.nwgeo", dir + "/custom.nwgeo"},
	} {
		if got := geoIPDestination(test.path, 6); got != test.want {
			t.Errorf("path %q: got %q, want %q", test.path, got, test.want)
		}
	}
}

func TestIntervalJoinsScanBeforeReturningToMenu(t *testing.T) {
	keys := make(chan rune, 1)
	keys <- 'M'
	session := Session{keys: keys, restore: func() {}}
	finished := make(chan struct{})
	scan := func(ctx context.Context, _ func(event.AccessEvent) error) (int, error) {
		defer close(finished)
		<-ctx.Done()
		return 0, ctx.Err()
	}
	now := time.Now()
	if err := session.RunInterval(t.Context(), scan, GeoOptions{}, now.Add(-time.Minute), now); !errors.Is(err, ErrMenu) {
		t.Fatalf("got %v, want menu transition", err)
	}
	select {
	case <-finished:
	default:
		t.Fatal("scan is still running")
	}
}

func TestMenuPreviewsAnimate(t *testing.T) {
	theme := themes[0]
	for _, card := range modeCards {
		first := strings.Join(menuPreview(card.mode, card.art, 25, 12, 0, theme), "\n")
		next := strings.Join(menuPreview(card.mode, card.art, 25, 12, 5, theme), "\n")
		if first == next {
			t.Errorf("%s preview did not change", card.mode)
		}
	}
	compact := menuRows(24, 7, 0, 0, 0, "", theme)
	if strings.Join(compact, "\n") != strings.Join(menuRows(24, 7, 0, 0, 5, "", theme), "\n") {
		t.Fatal("compact menu should be static")
	}
}

func TestHistoryDurationInput(t *testing.T) {
	for value, want := range map[string]time.Duration{
		"": DefaultHistoryDuration, "18:30": 18*time.Hour + 30*time.Minute,
		"18,30": 18*time.Hour + 30*time.Minute, "1830": 18*time.Hour + 30*time.Minute,
		"0130": 90 * time.Minute, "1h30m": 90 * time.Minute, "45s": 45 * time.Second,
		"00:00": 0, "12:60": 0, "1260": 0, "-1:30": 0, "1:+2": 0, "99999999:10": 0,
	} {
		got, err := parseHistoryDuration(value)
		if got != want || (err != nil) != (want == 0) {
			t.Errorf("%q got %v (%v), want %v", value, got, err, want)
		}
	}
}
