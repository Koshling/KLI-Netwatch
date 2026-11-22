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

// Fixed grids keep the map and timeline independent of the request count.
type overview struct {
	start, end time.Time
	world      [90][180]int
	timeline   [120]int
	located    int
	routes     bool
}

func newOverview(start, end time.Time) *overview { return &overview{start: start, end: end} }

func (o *overview) record(e event.AccessEvent, opts GeoOptions) {
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
	o.world[y][x]++
	o.located++
}

func RunInterval(ctx context.Context, scan func(context.Context, func(event.AccessEvent) error) (int, error), opts GeoOptions, start, end time.Time) error {
	d := newDashboard(opts)
	d.overview = newOverview(start, end)
	skipped, err := scan(ctx, func(e event.AccessEvent) error { d.add(e); return nil })
	if err != nil {
		return err
	}
	keys, restore := keyboardInput()
	defer restore()
	fmt.Print("\033[?25l")
	defer fmt.Print("\033[?25h\033[0m\n")
	return d.showOverview(ctx, keys, skipped)
}

func (d *Dashboard) showOverview(ctx context.Context, keys <-chan byte, skipped int) error {
	d.clearScreen = true
	counts := map[classifier.Category]int{}
	for _, category := range d.clients {
		counts[category]++
	}
	tick := time.NewTicker(350 * time.Millisecond)
	defer tick.Stop()
	for {
		w, h := terminalSize()
		if d.clearScreen || w != d.prevWidth || h != d.prevHeight {
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
			if (k == 'l' || k == 'L') && d.geoOptions.ServerSet {
				d.overview.routes = !d.overview.routes
				d.clearScreen = true
			}
			if k >= '1' && k <= '5' {
				d.themeIndex = int(k - '1')
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
	put(0, t.Accent+"Interval Overview")
	if w < 59 || h < 18 {
		put(1, "Resize to 60x18 or larger")
		return rows
	}
	put(1, t.Muted+strings.Repeat("═", w))
	put(2, t.UI+o.start.Format("2006-01-02 15:04:05 -0700")+"  →  "+o.end.Format("2006-01-02 15:04:05 -0700"))
	left := w * 2 / 3
	right := w - left - 3
	height := min(h-11, max(6, left/4))
	landMaskOnce.Do(buildLandMask)
	grid := make([]int, left*height)
	for y, line := range o.world {
		for x, n := range line {
			grid[(y*height/90)*left+x*left/180] += n
		}
	}
	serverCell := -1
	var routes []bool
	if d.geoOptions.ServerSet {
		sx := min(179, max(0, int((d.geoOptions.ServerLongitude+180)/2))) * left / 180
		sy := min(89, max(0, int((90-d.geoOptions.ServerLatitude)/2))) * height / 90
		serverCell = sy*left + sx
		if o.routes {
			routes = overviewRoutes(grid, left, height, serverCell)
		}
	}
	lines := []string{t.Accent + "REQUESTS / TIME"}
	bins := make([]int, min(right, len(o.timeline)))
	peak := 1
	for i, n := range o.timeline {
		j := i * len(bins) / len(o.timeline)
		bins[j] += n
		peak = max(peak, bins[j])
	}
	for level := 3; level >= 1; level-- {
		var b strings.Builder
		for _, n := range bins {
			if n > 0 && (3*n+peak-1)/peak >= level {
				b.WriteRune('█')
			} else {
				b.WriteRune('·')
			}
		}
		lines = append(lines, t.Data+b.String())
	}
	lines = append(lines, fmt.Sprintf("Peak %d / bin", peakIfTraffic(peak, d.total)), t.Accent+"HTTP STATUS")
	for i, name := range []string{"2xx", "3xx", "4xx", "5xx", "other"} {
		lines = append(lines, fmt.Sprintf("%-5s %d", name, d.statusCounts[i]))
	}
	lines = append(lines, t.Accent+"FINAL CLIENTS", fmt.Sprintf("USER %d", counts[classifier.User]), fmt.Sprintf("BOT %d", counts[classifier.Bot]), fmt.Sprintf("THREAT %d", counts[classifier.Threat]))
	for i := 0; i < h-6; i++ {
		var l string
		switch {
		case i == 0:
			l = t.Accent + "WORLD / REQUEST DENSITY"
		case i <= height:
			var b strings.Builder
			for x := 0; x < left; x++ {
				n := grid[(i-1)*left+x]
				switch {
				case (i-1)*left+x == serverCell:
					b.WriteString(t.Accent + "S")
				case n >= 10:
					b.WriteString(t.Accent + "@")
				case n > 0:
					b.WriteString(t.Data + "*")
				case routes != nil && routes[(i-1)*left+x]:
					b.WriteString(t.Muted + ".")
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
			l = t.Accent + "RECENT REQUESTS"

		default:
			j := len(d.feed) - 1 - (i - height - 3)
			if j >= 0 {
				e := d.feed[j].event
				l = fmt.Sprintf("%s %3d %s", e.Time.Format("15:04:05"), e.Status, strings.Map(func(r rune) rune {
					if r < 32 || r > 126 {
						return '?'
					}
					return r
				}, e.ClientIP))
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
	controls := "[Q] Exit   [1-5] Themes   " + t.Name
	if serverCell >= 0 {
		state := "off"
		if o.routes { state = "on" }
		controls = "[L] Lines " + state + "   " + controls
	}
	put(h-1, t.Accent+controls)
	return rows
}
func peakIfTraffic(peak, total int) int {
	if total == 0 {
		return 0
	}
	return peak
}

// Draw routes from the busiest visible cells, wrapping across the date line.
func overviewRoutes(grid []int, width, height, server int) []bool {
	var sources []int
	for cell, count := range grid {
		if count > 0 && cell != server {
			sources = append(sources, cell)
		}
	}
	sort.SliceStable(sources, func(i, j int) bool { return grid[sources[i]] > grid[sources[j]] })
	mask := make([]bool, len(grid))
	for _, cell := range sources[:min(len(sources), signalLimit(width,height))] {
		x, y := cell%width, cell/width
		dx, dy := server%width-x, server/width-y
		if dx > width/2 { dx -= width }
		if dx < -width/2 { dx += width }
		steps := max(dx, -dx, dy, -dy)
		for i := 1; i < steps; i++ {
			f := float64(i)/float64(steps)
			col := (width+x+int(math.Round(float64(dx)*f)))%width
			row := y+int(math.Round(float64(dy)*f))
			mask[row*width+col] = true
		}
	}
	return mask
}
