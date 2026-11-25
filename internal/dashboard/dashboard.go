package dashboard

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"netwatch/internal/classifier"
	"netwatch/internal/event"
)

const feedLimit = 100

type feedEvent struct {
	when     time.Time
	clientIP string
	method   string
	url      string
	status   int
	category classifier.Category
}

type geoKey struct {
	latitude  int
	longitude int
}

type Dashboard struct {
	screen
	overview         *overview
	replayStatus     string
	replayGlobeSpeed int
	bots, threats    int
	total            int
	success          int
	errors           int
	clients          map[string]classifier.Category
	classifier       *classifier.Classifier
	feed             []feedEvent
	feedStart        int
	angle            float64
	statusCounts     [5]int

	currentSecond time.Time
	currentCount  int
	rates         []int

	themeIndex     int
	hideGeoOverlay bool
	intervalOnly   bool

	geoOptions GeoOptions
	geoSeen    map[string]*geoKey
	geoMarkers map[geoKey]int
	signals    []geoMarker
	signalCap  int
	pending    []geoMarker
	recent     []geoMarker
}

func (s *Session) RunLive(ctx context.Context, events <-chan event.AccessEvent, options GeoOptions) error {
	return s.run(ctx, events, options, nil)
}

func newDashboard(options GeoOptions) *Dashboard {
	return &Dashboard{
		clients:    make(map[string]classifier.Category),
		classifier: classifier.New(),
		rates:      make([]int, 120),
		geoOptions: options,
		geoSeen:    make(map[string]*geoKey),
		geoMarkers: make(map[geoKey]int),
	}
}

func (s *Session) run(ctx context.Context, events <-chan event.AccessEvent, options GeoOptions, replay *replayClock) error {
	s.open()
	d := newDashboard(options)
	d.themeIndex = s.themeIndex
	layout := calculateLayout(terminalSize())
	d.signalCap = signalLimit(layout.GlobeWidth, layout.GlobeHeight)
	var stream *replayStream
	if replay != nil {
		stream = replay.openStream(ctx)
		events = stream.events
		defer func() { stream.close() }()
	}
	var endSince time.Time
	if replay != nil {
		d.overview = newOverview(replay.start, replay.end)
	}
	var shown time.Time
	if replay != nil {
		shown = replay.start
	}

	keys := s.keys

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	var pending *event.AccessEvent
	if replay != nil {
		d.updateRate(replay.anchor)
	}
	input := events
	var ready <-chan time.Time
	var wake *time.Timer
	if replay != nil {
		wake = time.NewTimer(time.Hour)
		wake.Stop()
		defer wake.Stop()
	}
	for {
		if replay != nil && replay.seeking {
			if d.catchUp(replay, pending, time.Now()) {
				pending = nil
				input = events
			}
			if !replay.seeking {
				d.signals, d.pending, d.recent = nil, nil, nil
				shown = replay.target
				// Use the target to vary the turn and keep each jump clearly visible.
				degrees := 90 + uint64(shown.Unix())%181
				d.angle = math.Mod(d.angle+float64(degrees)*math.Pi/180, 2*math.Pi)
				d.replayStatus = replay.status(d.currentSecond)
				d.replayGlobeSpeed = replay.globeSpeed()
				if err := d.render(); err != nil {
					return err
				}
			}
		}
		if wake != nil && pending != nil && !replay.paused && !replay.seeking {
			delay := pending.Time.Sub(replay.position(time.Now())) / time.Duration(replay.speed())
			wake.Reset(delay)
			ready = wake.C
		}
		select {
		case <-ready:
			d.updateRate(pending.Time)
			d.add(*pending)
			shown = pending.Time
			pending = nil
			ready = nil
			input = events
		case <-ctx.Done():
			return nil

		case e, ok := <-input:
			if !ok {
				input = nil
				if replay == nil {
					return nil
				}
				if replay != nil {
					<-stream.finished
					if stream.result.err != nil {
						return stream.result.err
					}
					replay.skipped = stream.result.skipped
					replay.sourceDone = true
				}
				continue
			}
			if replay != nil {
				pending = &e
				input = nil
			} else {
				d.updateRate(time.Now())
				d.add(e)
			}

		case key, ok := <-keys:
			if !ok {
				keys = nil
				continue
			}

			switch key {
			case '[', ']', keyLeft, keyRight:
				if replay != nil {
					step := time.Minute
					if key == '[' || key == keyLeft {
						step = -step
					}
					target := replay.seekTarget(shown, step)
					if (replay.seeking && target.Equal(replay.target)) || (!replay.seeking && target.Equal(shown)) {
						break
					}
					// Restart backward seeks, including direction changes during catch-up.
					if step < 0 {
						stream.close()
						stream = replay.openStream(ctx)
						events, input = stream.events, stream.events
						pending = nil
						replay.sourceDone = false
						replay.skipped = 0
						fresh := newDashboard(options)
						fresh.overview = newOverview(replay.start, replay.end)
						fresh.signalCap = d.signalCap
						fresh.hideGeoOverlay = d.hideGeoOverlay
						fresh.angle, fresh.themeIndex = d.angle, d.themeIndex
						d = fresh
						d.updateRate(replay.start)
					}
					endSince = time.Time{}
					replay.target, replay.seeking = target, true
				}
			case '1', '2', '3', '4', '5':
				// Reapply the theme background to the live summary after a partial update.
				d.themeIndex = s.selectTheme(key)
				d.clearScreen = true
			case 'f', 'F':
				d.hideGeoOverlay = !d.hideGeoOverlay
			case ' ':
				if replay != nil {
					replay.toggle(time.Now())
				}
			case '+', '=':
				if replay != nil {
					replay.changeSpeed(time.Now(), 1)
				}
			case '-':
				if replay != nil {
					replay.changeSpeed(time.Now(), -1)
				}
			case 'g':
				if replay != nil {
					replay.globeSpeedIndex = min(replay.globeSpeedIndex+1, len(replayGlobeSpeeds)-1)
				}
			case 'G':
				if replay != nil {
					replay.globeSpeedIndex = max(replay.globeSpeedIndex-1, 0)
				}
			case 'm', 'M':
				return ErrMenu
			case 'q', 'Q':
				return nil
			}

		case now := <-ticker.C:
			// Keep the previous frame visible while historical state is being rebuilt.
			if replay != nil && replay.seeking {
				break
			}
			if replay == nil || !replay.paused {
				d.advanceSignals()
				step := globeStep
				if replay != nil {
					step *= float64(replay.globeSpeed())
				}
				d.angle = math.Mod(d.angle+step, 2*math.Pi)
			}
			if replay == nil {
				d.updateRate(now)
			} else {
				position := replay.position(now)
				if replay.sourceDone && !replay.paused && !position.Before(replay.end) {
					if endSince.IsZero() {
						endSince = now
					}
					if now.Sub(endSince) >= 5*time.Second {
						return s.showOverview(ctx, d, keys, replay.skipped)
					}
				} else {
					endSince = time.Time{}
				}
				if !replay.seeking && ((pending != nil && pending.Time.After(position)) || replay.sourceDone) {
					shown = position
					d.updateRate(position)
				}
				d.replayStatus = replay.status(d.currentSecond)
				d.replayGlobeSpeed = replay.globeSpeed()
			}
			if err := d.render(); err != nil {
				return err
			}
		}
		if wake != nil {
			wake.Stop()
			ready = nil
		}
	}
}

func (d *Dashboard) add(e event.AccessEvent) {
	category := d.classifier.Classify(e)
	if d.overview != nil {
		d.overview.record(e, d.geoOptions, category)
	}
	d.total++
	d.currentCount++

	if e.ClientIP != "" {
		previous := d.clients[e.ClientIP]
		if category != previous {
			switch previous {
			case classifier.Bot:
				d.bots--
			case classifier.Threat:
				d.threats--
			}
			switch category {
			case classifier.Bot:
				d.bots++
			case classifier.Threat:
				d.threats++
			}
			d.clients[e.ClientIP] = category
		}

		if !d.intervalOnly && d.geoOptions.Resolver != nil {
			key, seen := d.geoSeen[e.ClientIP]
			if !seen {
				loc := d.geoOptions.Resolver.Lookup(e.ClientIP)
				if loc.Found {
					key = &geoKey{latitude: int(loc.Latitude * 100), longitude: int(loc.Longitude * 100)}
					d.geoMarkers[*key]++
				}
				d.geoSeen[e.ClientIP] = key
			}
			if key != nil {
				d.startSignal(*key, e.ClientIP, category, e.Time)
			}
		}
	}

	switch {
	case e.Status >= 200 && e.Status < 300:
		d.success++
		d.statusCounts[0]++
	case e.Status >= 300 && e.Status < 400:
		d.statusCounts[1]++
	case e.Status >= 400 && e.Status < 500:
		d.errors++
		d.statusCounts[2]++
	case e.Status >= 500:
		d.errors++
		d.statusCounts[3]++
	default:
		d.statusCounts[4]++
	}

	item := feedEvent{
		when: e.Time, clientIP: e.ClientIP, method: e.Method,
		url: e.URL, status: e.Status, category: category,
	}
	if len(d.feed) < feedLimit {
		d.feed = append(d.feed, item)
	} else {
		d.feed[d.feedStart] = item
		d.feedStart = (d.feedStart + 1) % feedLimit
	}
}

// feedAt indexes the retained requests from oldest to newest.
func (d *Dashboard) feedAt(index int) feedEvent {
	return d.feed[(d.feedStart+index)%len(d.feed)]
}

// Input from access logs is plain terminal text, including in JSON mode.
func printableField(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 || r >= 127 && r <= 159 {
			return '?'
		}
		return r
	}, s)
}

func fitANSI(s string, width int) string {
	var b strings.Builder
	visible := 0

	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			start := i
			i += 2

			for i < len(s) {
				c := s[i]
				i++
				if c >= 0x40 && c <= 0x7e {
					break
				}
			}

			b.WriteString(s[start:i])
			continue
		}

		_, size := utf8.DecodeRuneInString(s[i:])
		if size == 0 {
			break
		}

		if visible < width {
			b.WriteString(s[i : i+size])
		}
		visible++
		i += size
	}

	if visible < width {
		b.WriteString(strings.Repeat(" ", width-visible))
	}

	return b.String()
}

func (d *Dashboard) render() error {
	frame, layout := d.frame(terminalSize())
	// Dashboard rows already fill the screen width, including trailing spaces.
	// Overwrite them directly so a terminal never paints a blank row first.
	return d.writeRows(frame, layout, themes[d.themeIndex], false)
}

func (d *Dashboard) frame(width, height int) ([]string, Layout) {
	t := themes[d.themeIndex]
	l := calculateLayout(width, height)
	replay := d.replayStatus != ""

	left := make([]string, l.ContentRows)
	right := make([]string, l.ContentRows)

	left[0] = t.Accent + "GLOBAL TRAFFIC" + "\033[0m"

	d.signalCap = signalLimit(l.GlobeWidth, l.GlobeHeight)
	if len(d.signals) > d.signalCap {
		for _, signal := range d.signals[d.signalCap:] {
			d.enqueueSignal(signal, false)
		}
		d.signals = d.signals[:d.signalCap]
	}

	d.scheduleSignals()
	var server *geoMarker
	if d.geoOptions.ServerSet {
		server = &geoMarker{
			Latitude:  d.geoOptions.ServerLatitude,
			Longitude: d.geoOptions.ServerLongitude,
		}
	}

	var globe []string
	if d.hideGeoOverlay {
		globe = renderGlobe(l.GlobeWidth, l.GlobeHeight, d.angle, t)
	} else {
		places := append([]geoMarker(nil), d.pending...)
		places = append(places, d.recent...)
		globe = renderGlobeWithTraffic(
			l.GlobeWidth, l.GlobeHeight, d.angle, t, server, d.signals, d.currentSecond, places...,
		)
	}
	for i := 0; i < len(globe) && i+1 < l.ContentRows-2; i++ {
		left[i+1] = globe[i]
	}

	left[l.ContentRows-2] = t.Muted + geoStatus(d.geoOptions, len(d.geoMarkers)) + "\033[0m"
	serverText := "SERVER: NOT CONFIGURED"
	if d.geoOptions.ServerSet {
		serverText = fmt.Sprintf("SERVER: %.2f, %.2f",
			d.geoOptions.ServerLatitude, d.geoOptions.ServerLongitude)
	}
	left[l.ContentRows-1] = t.Muted + serverText + "\033[0m"

	row := 0
	put := func(line string) {
		if row < len(right) {
			right[row] = line
			row++
		}
	}

	feedTitle := "LIVE EVENT FEED"
	if replay {
		feedTitle = "REPLAY FEED"
	}
	put(t.Accent + feedTitle + "\033[0m")
	put(t.Muted + fmt.Sprintf("%-8s %-7s %-6s %-3s %s",
		"TIME", "TYPE", "METHOD", "CODE", "PATH") + "\033[0m")

	pathWidth := l.RightWidth - 29
	if pathWidth < 1 {
		pathWidth = 1
	}

	for i := 0; i < l.FeedRows; i++ {
		index := len(d.feed) - 1 - i
		if index < 0 {
			put("")
			continue
		}

		item := d.feedAt(index)
		path := []rune(printableField(item.url))

		if len(path) > pathWidth {
			if pathWidth > 3 {
				path = append(path[:pathWidth-3], '.', '.', '.')
			} else {
				path = path[:pathWidth]
			}
		}

		color := t.Data
		switch item.category {
		case classifier.Bot:
			color = "\033[36m"
		case classifier.Threat:
			color = "\033[31m"
		}

		put(fmt.Sprintf(
			"%s%-8s %-7s %-6s %-3d %s\033[0m",
			color,
			item.when.Format("15:04:05"),
			item.category,
			printableField(item.method),
			item.status,
			string(path),
		))
	}
	put(t.Muted + strings.Repeat("─", l.RightWidth-2) + "\033[0m")
	put(t.Accent + "REQUEST RATE / SEC" + "\033[0m")

	graph := d.rateGraph(l.GraphHeight, l.RightWidth-2, t)
	for _, line := range graph {
		put(line)
	}

	put(fmt.Sprintf("%sCURRENT: %d req/s\033[0m", t.UI, d.currentCount))
	put(t.Muted + strings.Repeat("─", l.RightWidth-2) + "\033[0m")

	botClients, threatClients := d.bots, d.threats
	userClients := len(d.clients) - botClients - threatClients

	shortCount := func(n int) string {
		switch {
		case n >= 1_000_000:
			return fmt.Sprintf("%.1fm", float64(n)/1_000_000)
		case n >= 1_000:
			return fmt.Sprintf("%.1fk", float64(n)/1_000)
		default:
			return fmt.Sprint(n)
		}
	}

	leftPanel := (l.RightWidth - 3) / 2
	if leftPanel < 1 {
		leftPanel = 1
	}

	put(fmt.Sprintf("%s%-*s%s │ %s%s",
		t.Accent, leftPanel, "HTTP STATUS",
		t.Muted, t.Accent, "CLIENTS BY TYPE"))

	if l.StatusRows == 2 {
		httpStats := fmt.Sprintf("2:%s 3:%s 4:%s 5:%s O:%s",
			shortCount(d.statusCounts[0]),
			shortCount(d.statusCounts[1]),
			shortCount(d.statusCounts[2]),
			shortCount(d.statusCounts[3]),
			shortCount(d.statusCounts[4]),
		)
		clientStats := fmt.Sprintf("U:%s B:%s T:%s",
			shortCount(userClients),
			shortCount(botClients),
			shortCount(threatClients),
		)

		put(fmt.Sprintf("%s%-*s%s │ %s%s",
			t.Data, leftPanel, httpStats,
			t.Muted, t.Data, clientStats))
	} else {
		put(fmt.Sprintf("%s%-*s%s │ %s%s",
			t.Data, leftPanel,
			fmt.Sprintf("2xx:%s 3xx:%s",
				shortCount(d.statusCounts[0]),
				shortCount(d.statusCounts[1])),
			t.Muted, t.Data,
			fmt.Sprintf("USER: %s BOT: %s",
				shortCount(userClients),
				shortCount(botClients))))

		put(fmt.Sprintf("%s%-*s%s │ %s%s",
			t.Data, leftPanel,
			fmt.Sprintf("4xx:%s 5xx:%s O:%s",
				shortCount(d.statusCounts[2]),
				shortCount(d.statusCounts[3]),
				shortCount(d.statusCounts[4])),
			t.Muted, t.Data,
			fmt.Sprintf("THREAT: %s", shortCount(threatClients))))
	}

	frame := make([]string, 0, l.Height)

	summary := fmt.Sprintf(
		"%sREQ %d   CLIENTS %d   USER %d   BOT %d   THREAT %d",
		t.Accent, d.total, len(d.clients),
		userClients, botClients, threatClients,
	)
	frame = append(frame, fitANSI(summary, l.Width))
	frame = append(frame, fitANSI(t.Muted+strings.Repeat("═", l.Width), l.Width))

	for i := 0; i < l.ContentRows; i++ {
		leftLine := fitANSI(left[i], l.LeftWidth)
		rightLine := fitANSI(right[i], l.RightWidth)
		frame = append(frame, leftLine+t.Muted+" │ "+t.UI+rightLine)
	}

	frame = append(frame, fitANSI(t.Muted+strings.Repeat("═", l.Width), l.Width))
	controls := fitANSI(t.Accent+"[1] MONO  [2] MATRIX  [3] TERMINAL  [4] WESTERN  [5] REVERSE MONO", l.Width)
	geoToggle := "[F] Overlay on"
	if d.hideGeoOverlay {
		geoToggle = "[F] Overlay off"
	}
	footer := fitANSI(t.UI+"[M] Menu  [Q] Exit  "+geoToggle, l.Width)
	if replay {
		controls = withRightLabel(t.Accent+d.replayStatus, "Themes 1-5", t.Accent, l.Width)
		globe := fmt.Sprintf("[M] Menu [Q] Exit %s [g/G] %dx [←/→] ±1m", geoToggle, d.replayGlobeSpeed)
		footer = withRightLabel(t.UI+globe, "THEME: "+t.Name, t.Accent, l.Width)
	}
	frame = append(frame, controls, footer)
	return frame, l
}

// Labels contain ASCII theme names. Reserve their columns before clipping the left text.
func withRightLabel(left, label, color string, width int) string {
	if width < len(label)+3 {
		return fitANSI(left, width)
	}
	return fitANSI(left, width-len(label)-3) + "\033[0m  " + color + label + " "
}

// Inner resets end a styled span but must preserve the white canvas.
var reverseMonoSpans = strings.NewReplacer(
	"\033[0m", "\033[0;30;47m",
	"\033[36m", "\033[2;30;47m", // Bot
	"\033[31m", "\033[1;30;47m", // Threat
)

// screen caches the last frame so that unchanged rows need no terminal writes.
type screen struct {
	clearScreen bool
	prevFrame   []string
	prevWidth   int
	prevHeight  int
}

func (s *screen) writeFrame(frame []string, l Layout, t Theme) error {
	return s.writeRows(frame, l, t, true)
}

func (s *screen) writeRows(frame []string, l Layout, t Theme, eraseRows bool) error {
	geometryChanged := s.prevWidth != l.Width ||
		s.prevHeight != l.Height ||
		len(s.prevFrame) != len(frame)

	fullRedraw := s.clearScreen || geometryChanged
	var b strings.Builder

	if fullRedraw {
		b.WriteString(t.Background)
		if geometryChanged || len(frame) < l.Height {
			b.WriteString("\033[2J")
		}
	}

	for i, line := range frame {
		if !fullRedraw && s.prevFrame[i] == line {
			continue
		}

		fmt.Fprintf(&b, "\033[%d;1H", i+1)
		b.WriteString(t.UI)
		if eraseRows {
			b.WriteString("\033[K")
		}
		if t.Name == "REVERSE MONO" {
			reverseMonoSpans.WriteString(&b, line)
		} else {
			b.WriteString(line)
		}
		b.WriteString("\033[0m")
	}

	if b.Len() == 0 {
		return nil
	}
	if _, err := os.Stdout.WriteString(b.String()); err != nil {
		return err
	}

	s.prevFrame = append(s.prevFrame[:0], frame...)
	s.prevWidth = l.Width
	s.prevHeight = l.Height
	s.clearScreen = false

	return nil
}

func (d *Dashboard) rateGraph(height, width int, t Theme) []string {
	result := make([]string, height)
	maxValue := 1

	for _, value := range d.rates {
		if value > maxValue {
			maxValue = value
		}
	}

	usable := width
	if usable > len(d.rates) {
		usable = len(d.rates)
	}

	for row := 0; row < height; row++ {
		threshold := maxValue * (height - row) / height
		var line strings.Builder

		for i := len(d.rates) - usable; i < len(d.rates); i++ {
			if d.rates[i] >= threshold && d.rates[i] > 0 {
				line.WriteString(t.Data + "█" + "\033[0m")
			} else {
				line.WriteString(t.Muted + "·" + "\033[0m")
			}
		}

		result[row] = line.String()
	}

	return result
}

func (d *Dashboard) startSignal(key geoKey, source string, category classifier.Category, seen time.Time) {
	marker := geoMarker{Latitude: float64(key.latitude)/100, Longitude: float64(key.longitude)/100, Source: source, Category: category, Seen: seen}
	for i := range d.signals {
		if d.signals[i].samePlace(marker) {
			d.signals[i].Seen = seen
		}
	}
	for i := range d.recent {
		if d.recent[i].samePlace(marker) {
			d.recent[i].Seen = seen
			d.recent[i].Category = category
			break
		}
	}
	d.enqueueSignal(marker, true)
}

func (d *Dashboard) enqueueSignal(marker geoMarker, newRequest bool) {
	marker.Age = 0
	for i, queued := range d.pending {
		if queued.Source == marker.Source {
			// A displaced pulse predates the request already waiting for this IP.
			if newRequest {
				d.pending[i] = marker
			}
			return
		}
	}
	if len(d.pending) < 512 {
		d.pending = append(d.pending, marker)
	}
}

func (d *Dashboard) scheduleSignals() {
	recent := d.recent[:0]
	for _, marker := range d.recent {
		if d.currentSecond.Sub(marker.Seen) < 30*time.Second {
			recent = append(recent, marker)
		}
	}
	d.recent = recent
	waiting := d.pending[:0]
	for _, marker := range d.pending {
		if !marker.Seen.IsZero() && d.currentSecond.Sub(marker.Seen) >= 2*time.Minute {
			continue
		}
		active := false
		for _, signal := range d.signals {
			if signal.Source == marker.Source {
				active = true
				break
			}
		}
		if active || len(d.signals) >= d.signalCap || !marker.canLaunch(d.angle, max(1, d.replayGlobeSpeed)) {
			waiting = append(waiting, marker)
			continue
		}
		d.signals = append(d.signals, marker)
		for i, old := range d.recent {
			if old.samePlace(marker) {
				d.recent = append(d.recent[:i], d.recent[i+1:]...)
				break
			}
		}
		d.recent = append(d.recent, marker)
	}
	d.pending = waiting
}

func (d *Dashboard) advanceSignals() {
	active := d.signals[:0]
	for _, signal := range d.signals {
		signal.Age++
		if signal.Age < signalFrames {
			active = append(active, signal)
		}
	}
	d.signals = active
}

const globeStep = 0.035

// Reserve a short visibility window so a queued source cannot spend its
// first frames grazing the silhouette as the globe rotates away.
func (m geoMarker) canLaunch(angle float64, rotationSpeed int) bool {
	lat := m.Latitude * math.Pi / 180
	lon := m.Longitude * math.Pi / 180 + angle
	height := math.Sin(lat) * math.Sin(globeTilt)
	radius := math.Cos(lat) * math.Cos(globeTilt)
	for frame := 0; frame < 3; frame++ {
		depth := height + radius*math.Cos(lon+float64(frame*rotationSpeed)*globeStep)
		if depth < 0.4 {
			return false
		}
	}
	return true
}
