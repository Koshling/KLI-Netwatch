package dashboard

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"netwatch/internal/classifier"
	"netwatch/internal/event"
	"netwatch/internal/geoip"
	"time"
)

func TestReplayClockPauseAndSpeed(t *testing.T) {
	now := time.Unix(1000, 0)
	start := time.Unix(100, 0)
	c := replayClock{anchor: start, wall: now, end: start.Add(time.Minute)}
	c.changeSpeed(now.Add(time.Second), 1)
	c.toggle(now.Add(2 * time.Second))
	if got := c.position(now.Add(time.Minute)); !got.Equal(start.Add(6 * time.Second)) {
		t.Fatalf("paused position = %s", got)
	}
	c.toggle(now.Add(time.Minute))
	if got := c.position(now.Add(61 * time.Second)); !got.Equal(start.Add(11 * time.Second)) {
		t.Fatalf("resumed position = %s", got)
	}
}

func TestReplayRateGap(t *testing.T) {
	start := time.Unix(100, 0)
	d := Dashboard{rates: make([]int, 4)}
	d.advanceReplayRate(start)
	d.currentCount = 7
	d.advanceReplayRate(start.Add(3 * time.Second))
	if d.rates[1] != 7 || d.rates[3] != 0 || d.currentCount != 0 {
		t.Fatalf("rates = %v, active count = %d", d.rates, d.currentCount)
	}
	d.advanceReplayRate(start.Add(time.Hour))
	for _, count := range d.rates {
		if count != 0 {
			t.Fatalf("rates after long gap = %v", d.rates)
		}
	}
}

func TestReplayEventUpdatesGlobe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ipv4.csv")
	data := "134744064,134744319,US,United States,California,Los Angeles,34.05,-118.24,,\n"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	resolver, err := geoip.Open(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer resolver.Close()

	d := Dashboard{
		clients:    make(map[string]classifier.Category),
		classifier: classifier.New(),
		geoOptions: GeoOptions{Resolver: resolver},
		geoSeen:    make(map[string]*geoKey),
		geoMarkers: make(map[geoKey]int),
	}
	start := time.Unix(100, 0)
	d.advanceReplayRate(start)
	d.add(event.AccessEvent{Time: start, ClientIP: "8.8.8.8", Status: 200})
	d.advanceReplayRate(start.Add(time.Second))
	d.add(event.AccessEvent{Time: start.Add(time.Second), ClientIP: "8.8.8.8", Status: 200})
	summary := newDashboard(d.geoOptions)
	summary.intervalOnly = true
	summary.overview = newOverview(start, start.Add(time.Minute))
	summary.add(event.AccessEvent{Time: start, ClientIP: "8.8.8.8", Status: 200})
	if summary.overview.located != 1 || len(summary.pending) != 0 || len(summary.geoSeen) != 0 {
		t.Fatal("interval scan must aggregate mapped traffic without tracking globe sources")
	}
	const want = 1
	if len(d.geoMarkers) != want {
		t.Fatalf("replay globe marker count = %d, want %d", len(d.geoMarkers), want)
	}
	for _, count := range d.geoMarkers {
		if count != want {
			t.Fatalf("unique IPs at marker = %d, want %d", count, want)
		}
	}
}

func TestSeekMatchesChronologicalPass(t *testing.T) {
	start := time.Unix(100, 0)
	target := start.Add(time.Minute)
	rows := []event.AccessEvent{
		{Time: start, ClientIP: "8.8.8.8", Status: 200},
		{Time: target, ClientIP: "1.1.1.1", URL: "/.env", Status: 403},
		{Time: target, ClientIP: "8.8.8.8", Status: 200},
		{Time: target.Add(time.Second), ClientIP: "9.9.9.9", Status: 500},
	}
	want := newDashboard(GeoOptions{})
	want.advanceReplayRate(start)
	for _, e := range rows[:3] {
		want.advanceReplayRate(e.Time)
		want.add(e)
	}
	want.advanceReplayRate(target)
	rebuilt := newDashboard(GeoOptions{})
	rebuilt.advanceReplayRate(start)
	c := replayClock{start: start, end: start.Add(time.Hour), target: target, seeking: true, paused: true}
	for i := range rows {
		if consumed := rebuilt.catchUp(&c, &rows[i], start); consumed != (i < 3) {
			t.Fatalf("unexpected consumption at row %d", i)
		}
	}
	if c.seeking || !c.paused || !c.anchor.Equal(target) || !reflect.DeepEqual(want, rebuilt) {
		t.Fatal("seek state differs from linear playback")
	}
}

func TestSeekKeysAndProfileBoundaries(t *testing.T) {
	for keys, want := range map[string]byte{"[": '[', "]": ']', "\x1b[D": '[', "\x1bOC": ']'} {
		var state, got byte
		for _, key := range []byte(keys) {
			got = decodeSeekKey(&state, key)
		}
		if got != want {
			t.Fatalf("key %q decoded as %q", keys, got)
		}
	}
	c := replayClock{globeSpeedIndex: 2}
	c.changeSpeed(time.Now(), -1)
	if c.speed() != 1 || c.globeSpeed() != 2 {
		t.Fatal("minimum replay speed blocked globe")
	}
	c.speedIndex = len(replaySpeeds) - 1
	c.changeSpeed(time.Now(), 1)
	if c.speed() != 100 || c.globeSpeed() != 4 {
		t.Fatal("maximum replay speed blocked globe")
	}
}

func TestCancelUnreadReplayStream(t *testing.T) {
	started := make(chan struct{})
	clock := replayClock{scan: func(ctx context.Context, emit func(event.AccessEvent) error) (int, error) {
		close(started)
		return 0, emit(event.AccessEvent{})
	}}
	stream := clock.openStream(context.Background())
	<-started
	stream.close()
	if stream.result.err != context.Canceled {
		t.Fatal(stream.result.err)
	}
}

func TestReplayFrameFits80x24(t *testing.T) {
	d := newDashboard(GeoOptions{})
	d.replayGlobeSpeed = 16
	d.replayStatus = "REPLAY PAUSE 14:15:16 100x [SPACE] pause [+/-] speed | skipped 123"
	ansi := regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]")
	for index, theme := range themes {
		d.themeIndex = index
		frame, layout := d.frame(80, 24)
		if len(frame) != 24 || layout.ContentRows != 19 {
			t.Fatalf("theme %s: frame has %d rows, content has %d", theme.Name, len(frame), layout.ContentRows)
		}
		for row, line := range frame {
			if cells := utf8.RuneCountInString(ansi.ReplaceAllString(line, "")); cells != 80 {
				t.Fatalf("theme %s: row %d has %d columns", theme.Name, row+1, cells)
			}
		}
		for _, row := range []int{1, 21} {
			if ansi.ReplaceAllString(frame[row], "") != strings.Repeat("═", 80) {
				t.Fatalf("theme %s: missing separator on row %d", theme.Name, row+1)
			}
		}
		if !strings.HasSuffix(strings.TrimRight(frame[22], " "), "Themes 1-5") || !strings.HasSuffix(strings.TrimRight(frame[23], " "), "THEME: "+theme.Name) ||
			!strings.Contains(frame[22], "[+/-] speed") || !strings.Contains(frame[23], "[Q] Exit") {
			t.Fatalf("theme %s: footer controls or labels missing", theme.Name)
		}
	}
}

func TestLiveFooterAndFeed(t *testing.T) {
	d := newDashboard(GeoOptions{})
	frame, _ := d.frame(80, 24)
	if !strings.Contains(frame[2], "LIVE EVENT FEED") {
		t.Fatal("Live event feed title changed")
	}
	if !strings.Contains(frame[22], "[1] MONO") ||
		!strings.Contains(frame[23], "[Q] Exit") {
		t.Fatal("Live footer changed")
	}
}

func TestOverlayFooterInLiveAndReplay(t *testing.T) {
	d := newDashboard(GeoOptions{})
	for _, status := range []string{"", "REPLAY"} {
		d.replayStatus = status
		for _, hidden := range []bool{false, true} {
			d.hideGeoOverlay = hidden
			want := "[F] Overlay on"
			if hidden {
				want = "[F] Overlay off"
			}
			rows, _ := d.frame(80, 24)
			if !strings.Contains(rows[23], want) {
				t.Fatalf("status %q: missing %q in footer", status, want)
			}
		}
	}
}
