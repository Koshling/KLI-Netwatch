package dashboard

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"netwatch/internal/classifier"
	"netwatch/internal/event"
)

// Allocate latitude bands only where requests have been located.
const densityBandHeight = 15

// Fixed grids keep the map and timeline independent of the request count.
type overview struct {
	start, end time.Time
	world      [90 / densityBandHeight]*[densityBandHeight][180]density
	timeline   [120]int
	located    int
	routes     int
}

func newOverview(start, end time.Time) *overview { return &overview{start: start, end: end} }

func (o *overview) record(e event.AccessEvent, opts GeoOptions, category classifier.Category) {
	fraction := float64(e.Time.Sub(o.start)) / float64(o.end.Sub(o.start))
	o.timeline[min(119, max(0, int(fraction*120)))]++
	if opts.Resolver == nil {
		return
	}
	loc := opts.Resolver.Lookup(e.ClientIP)
	if !loc.Found {
		return
	}
	x := min(179, max(0, int((loc.Longitude+180)/2)))
	y := min(89, max(0, int((90-loc.Latitude)/2)))
	index := 0
	if category == classifier.Bot { index = 1 }
	if category == classifier.Threat { index = 2 }
	band := y / densityBandHeight
	if o.world[band] == nil {
		o.world[band] = new([densityBandHeight][180]density)
	}
	o.world[band][y%densityBandHeight][x][index]++
	o.located++
}

func (s *Session) RunInterval(ctx context.Context, scan func(context.Context, func(event.AccessEvent) error) (int, error), opts GeoOptions, start, end time.Time) error {
	s.open()
	d := newDashboard(opts)
	d.intervalOnly = true
	d.overview = newOverview(start, end)

	// Keep navigation available during scans, and wait for the scanner before
	// handing control back to the caller that owns the log file.
	scanCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	var result scanResult
	go func() {
		defer close(done)
		result.skipped, result.err = scan(scanCtx, func(e event.AccessEvent) error {
			d.add(e)
			return nil
		})
	}()
	defer func() {
		cancel()
		<-done
	}()

	keys := s.keys
	var view screen
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		w, h := terminalSize()
		t := themes[s.themeIndex]
		rows := []string{fitANSI(t.heading()+"INTERVAL  [M] Menu  [Q] Exit", w)}
		if h > 1 {
			rows = append(rows, fitANSI(t.UI+"Scanning historical log...", w))
		}
		if err := view.writeFrame(rows, Layout{Width: w, Height: h}, t); err != nil {
			return err
		}
		select {
		case <-done:
			if ctx.Err() != nil {
				return nil
			}
			if result.err != nil {
				return result.err
			}
			d.themeIndex = s.themeIndex
			return s.showOverview(ctx, d, keys, result.skipped)
		case <-ctx.Done():
			return nil
		case key, ok := <-keys:
			if !ok || key == 'q' || key == 'Q' {
				return nil
			}
			switch key {
			case 'm', 'M':
				return ErrMenu
			case '1', '2', '3', '4', '5', '6', '7':
				s.selectTheme(key)
				view.clearScreen = true
			}
		case <-tick.C:
		}
	}
}

func (s *Session) showOverview(ctx context.Context, d *Dashboard, keys <-chan rune, skipped int) error {
	d.clearScreen = true
	counts := map[classifier.Category]int{}
	for _, category := range d.clients {
		counts[category]++
	}
	dirty := true
	tick := time.NewTicker(350 * time.Millisecond)
	defer tick.Stop()
	for {
		w, h := terminalSize()
		if dirty || d.clearScreen || w != d.prevWidth || h != d.prevHeight {
			dirty = false
			rows := d.overviewRows(w, h, skipped, counts)
			if err := d.writeFrame(rows, Layout{Width: w, Height: h}, themes[d.themeIndex]); err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case k, ok := <-keys:
			if !ok || k == 'q' || k == 'Q' {
				return nil
			}
			if k == 'm' || k == 'M' {
				return ErrMenu
			}
			if (k == 'l' || k == 'L') && d.geoOptions.ServerSet {
				d.overview.routes = (d.overview.routes + 1) % 4
				dirty = true
			}
			if k >= '1' && k <= '7' && int(k-'1') != d.themeIndex {
				d.themeIndex = s.selectTheme(k)
				d.clearScreen = true
			}
		case <-tick.C:
		}
	}
}

func (d *Dashboard) overviewRows(w, h, skipped int, counts map[classifier.Category]int) []string {
	o, t := d.overview, themes[d.themeIndex]
	w = max(1, w-1)
	rows := make([]string, max(1, h))
	put := func(i int, s string) {
		if i >= 0 && i < len(rows) {
			rows[i] = fitANSI(s, w)
		}
	}
	put(0, t.heading()+"Interval Overview")
	if w < 59 || h < 18 {
		put(1, "[M] Menu  [Q] Exit")
		put(2, "Resize to 60x18 or larger")
		return rows
	}
	put(1, t.Muted+strings.Repeat("═", w))
	put(2, t.UI+o.start.Format("2006-01-02 15:04:05 -0700")+"  →  "+o.end.Format("2006-01-02 15:04:05 -0700"))
	left := w * 2 / 3
	right := w - left - 3
	height := min(h-11, max(6, left/4))
	landMaskOnce.Do(buildLandMask)
	grid := make([]density, left*height)
	for bandIndex, band := range o.world {
		if band == nil {
			continue
		}
		for row := range band {
			y := bandIndex*densityBandHeight + row
			for x, n := range band[row] {
				grid[(y*height/90)*left+x*left/180].add(n)
			}
		}
	}
	serverCell := -1
	var routes []globeCell
	if d.geoOptions.ServerSet {
		sx := min(179, max(0, int((d.geoOptions.ServerLongitude+180)/2))) * left / 180
		sy := min(89, max(0, int((90-d.geoOptions.ServerLatitude)/2))) * height / 90
		serverCell = sy*left + sx
		if o.routes != 0 {
			routes = overviewRoutes(grid, left, height, serverCell, o.routes >= 2)
		}
	}
	lines := []string{t.heading() + "REQUESTS / TIME"}
	bins := make([]int, min(right, len(o.timeline)))
	peak := 1
	for i, n := range o.timeline {
		j := i * len(bins) / len(o.timeline)
		bins[j] += n
		peak = max(peak, bins[j])
	}
	levels := 3
	if h < 21 {
		levels = 1
		lines[0] = fmt.Sprintf("Peak %d / bin", peakIfTraffic(peak, d.total))
	}
	for level := levels; level >= 1; level-- {
		var b strings.Builder
		for _, n := range bins {
			if levels == 1 && n > 0 {
				const bars = "▁▂▃▄▅▆▇█"
				b.WriteRune([]rune(bars)[(int64(n)*8-1)/int64(peak)])
			} else if n > 0 && (3*n+peak-1)/peak >= level {
				b.WriteRune('█')
			} else {
				b.WriteRune('·')
			}
		}
		lines = append(lines, t.Data+b.String())
	}
	if levels > 1 {
		lines = append(lines, fmt.Sprintf("Peak %d / bin", peakIfTraffic(peak, d.total)))
	}
	lines = append(lines, t.heading()+"HTTP STATUS")
	for i, name := range []string{"2xx", "3xx", "4xx", "5xx", "other"} {
		lines = append(lines, fmt.Sprintf("%-5s %d", name, d.statusCounts[i]))
	}
	lines = append(lines, t.heading()+"FINAL CLIENTS", fmt.Sprintf("USER %d", counts[classifier.User]), fmt.Sprintf("BOT %d", counts[classifier.Bot]), fmt.Sprintf("THREAT %d", counts[classifier.Threat]))
	for i := 0; i < h-6; i++ {
		var l string
		switch {
		case i == 0:
			l = t.heading() + "WORLD / REQUEST DENSITY"
		case i <= height:
			var b strings.Builder
			for x := 0; x < left; x++ {
				n := grid[(i-1)*left+x].total()
				switch {
				case (i-1)*left+x == serverCell:
					b.WriteString(t.Accent + "S")
				case n >= 10:
					b.WriteString(t.Accent + "@")
				case n > 0:
					b.WriteString(t.Data + "*")
				case routes != nil && routes[(i-1)*left+x].glyph != 0:
					route := routes[(i-1)*left+x]
					b.WriteString(pulseColor(route.category, t))
					if o.routes == 3 && isLand((float64(x)+0.5)*360/float64(left)-180, 90-(float64(i)-0.5)*180/float64(height)) {
						b.WriteRune('░')
					} else {
						b.WriteRune(route.glyph)
					}
					b.WriteString("\033[0m")
				case isLand((float64(x)+0.5)*360/float64(left)-180, 90-(float64(i)-0.5)*180/float64(height)):
					b.WriteString(t.Muted + "░")
				default:
					b.WriteByte(' ')
				}
			}
			l = b.String()
		case i == height+1:
			l = t.Data + "* 1-9   @ 10+ requests"
			if serverCell >= 0 { l += "   S server" }
		case i == height+2:
			l = t.heading() + "RECENT REQUESTS"

		default:
			j := len(d.feed) - 1 - (i - height - 3)
			if j >= 0 {
				e := d.feedAt(j)
				l = fmt.Sprintf("%s %3d %s", e.when.Format("15:04:05"), e.status, strings.Map(func(r rune) rune {
					if r < 32 || r > 126 {
						return '?'
					}
					return r
				}, e.clientIP))
			}
		}
		r := ""
		if i < len(lines) {
			r = lines[i]
		}
		put(i+3, fitANSI(t.UI+l, left)+t.Muted+" │ "+t.UI+r)
	}
	put(h-3, t.UI+fmt.Sprintf("Requests %d   Clients %d   Mapped %d   Skipped lines %d", d.total, len(d.clients), o.located, skipped))
	put(h-2, t.Muted+strings.Repeat("═", w))
	controls := "[M] Menu   [Q] Exit   "
	if serverCell >= 0 {
		state := [...]string{"off", "dots", "braille", "efficient"}[o.routes]
		controls += "[L] Lines " + state + "   "
	}
	put(h-1, t.Accent+controls+"[1-7] Themes   "+t.Name)
	return rows
}
func peakIfTraffic(peak, total int) int {
	if total == 0 {
		return 0
	}
	return peak
}

type density [3]int

func (d density) total() int { return d[0]+d[1]+d[2] }
func (d *density) add(other density) {
	for i, n := range other { d[i] += n }
}

type routeGroup struct {
	counts density
	xSum, ySum float64
	cells []int
}

func overviewRoutes(grid []density, width, height, server int, braille bool) []globeCell {
	// Group 6 columns by 3 rows, accounting for the shape of terminal cells.
	stride := (width+5)/6
	groups := make([]routeGroup, stride*((height+2)/3))
	for cell, counts := range grid {
		n := counts.total()
		if n == 0 || cell == server { continue }
		x, y := cell%width, cell/width
		g := &groups[(y/3)*stride+x/6]
		g.counts.add(counts)
		g.xSum += float64(x)*float64(n)
		g.ySum += float64(y)*float64(n)
		g.cells = append(g.cells, cell)
	}
	sort.SliceStable(groups, func(i,j int) bool { return groups[i].counts.total() > groups[j].counts.total() })
	mask := make([]globeCell, len(grid))
	cols, rows := 1, 1
	if braille { cols, rows = 2, 4 }
	bits := [4][2]rune{{1, 8}, {2, 16}, {4, 32}, {64, 128}}
	for _, g := range groups[:min(len(groups), signalLimit(width,height))] {
		total := float64(g.counts.total())
		if total == 0 { break }
		cx, cy := g.xSum/total, g.ySum/total
		cell, best := 0, math.Inf(1)
		for _, candidate := range g.cells {
			dx, dy := float64(candidate%width)-cx, 2*(float64(candidate/width)-cy)
			if distance := dx*dx+dy*dy; distance < best {
				cell, best = candidate, distance
			}
		}
		category := classifier.User
		if g.counts[1] > g.counts[0] { category = classifier.Bot }
		if g.counts[2] >= max(g.counts[0],g.counts[1]) { category = classifier.Threat }
		x, y := cell%width, cell/width
		dx, dy := server%width-x, server/width-y
		if dx > width/2 { dx -= width }
		if dx < -width/2 { dx += width }
		x, y = x*cols+cols/2, y*rows+rows/2
		dx, dy = dx*cols, dy*rows
		steps := max(dx, -dx, dy, -dy)
		for i := 1; i < steps; i++ {
			f := float64(i)/float64(steps)
			col := (width*cols+x+int(math.Round(float64(dx)*f)))%(width*cols)
			row := y+int(math.Round(float64(dy)*f))
			index := (row/rows)*width + col/cols
			if mask[index].category == "" { mask[index].category = category }
			if braille {
				mask[index].glyph |= 0x2800 | bits[row%4][col%2]
			} else {
				mask[index].glyph = '.'
			}
		}
	}
	return mask
}
