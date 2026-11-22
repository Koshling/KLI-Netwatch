package dashboard

import (
	"fmt"
	"math"
	"strings"
	"time"

	"netwatch/internal/geoip"
)

type GeoOptions struct {
	Resolver        *geoip.Resolver
	ServerLatitude  float64
	ServerLongitude float64
	ServerSet       bool
}

type geoMarker struct {
	Latitude  float64
	Longitude float64
	Age       int
	Seen      time.Time
}

func (m geoMarker) samePlace(other geoMarker) bool {
	return m.Latitude == other.Latitude && m.Longitude == other.Longitude
}

type screenPoint struct {
	x, y  int
	depth float64
}

func renderGlobeWithTraffic(
	width, height int,
	angle float64,
	theme Theme,
	server *geoMarker,
	visitors []geoMarker,
	recent ...geoMarker,
) []string {
	lines := renderGlobe(width, height, angle, theme)
	if width < 2 || height < 2 || (server == nil && len(visitors) == 0 && len(recent) == 0) {
		return lines
	}

	cx := float64(width-1) / 2
	cy := float64(height-1) / 2
	rx := float64(width) * 0.46
	ry := float64(height) * 0.47

	if aspect := rx / (2 * ry); aspect > globeMaxAspect {
		rx = 2 * ry * globeMaxAspect
	} else if aspect < globeMinAspect {
		ry = rx / (2 * globeMinAspect)
	}

	view := globeView{
		lon0:    -angle * 180 / math.Pi,
		sinTilt: math.Sin(globeTilt),
		cosTilt: math.Cos(globeTilt),
	}

	overlay := make([][]rune, height)
	for y := range overlay {
		overlay[y] = make([]rune, width)
	}

	plot := func(x, y int, char rune) {
		if x < 0 || x >= width || y < 0 || y >= height {
			return
		}
		old := overlay[y][x]
		if old == 'S' {
			return
		}
		if char == 'S' || old == 0 || old == ' ' || char == '*' || char == '+' {
			overlay[y][x] = char
		}
	}

	project := func(lon, lat float64) screenPoint {
		latRad := lat * math.Pi / 180
		delta := (lon - view.lon0) * math.Pi / 180

		cosLat := math.Cos(latRad)
		nx := cosLat * math.Sin(delta)
		zz := cosLat * math.Cos(delta)
		sinLat := math.Sin(latRad)

		ny := -sinLat*view.cosTilt + zz*view.sinTilt
		depth := sinLat*view.sinTilt + zz*view.cosTilt

		return screenPoint{
			x:     int(math.Round(cx + nx*rx)),
			y:     int(math.Round(cy + ny*ry)),
			depth: depth,
		}
	}

	limit := max(2, signalLimit(width, height)*3-1)
	marked := 0
	for _, visitor := range visitors {
		p := project(visitor.Longitude, visitor.Latitude)
		if p.depth > 0 && p.y >= 0 && p.y < height && p.x >= 0 && p.x < width && overlay[p.y][p.x] == 0 {
			plot(p.x, p.y, 'o')
			marked++
		}
	}
	for i := len(recent)-1; i >= 0 && marked < limit; i-- {
		p := project(recent[i].Longitude, recent[i].Latitude)
		if p.depth > 0 && p.y >= 0 && p.y < height && p.x >= 0 && p.x < width && overlay[p.y][p.x] == 0 {
			plot(p.x, p.y, '@')
			marked++
		}
	}
	for _, visitor := range visitors {
		// Leave a quiet frame between flashes, including in marker-only mode.
		if visitor.Age%4 == 3 {
			continue
		}
		if server == nil {
			p := project(visitor.Longitude, visitor.Latitude)
			if p.depth > 0 {
				plot(p.x, p.y, '*')
			}
			continue
		}
		drawGeoArc(visitor.Latitude, visitor.Longitude, server.Latitude, server.Longitude, visitor.Age, project, plot)
	}

	if server != nil {
		p := project(server.Longitude, server.Latitude)
		if p.depth > 0 {
			plot(p.x, p.y, 'S')
		}
	}

	for y := range lines {
		if y >= len(overlay) {
			break
		}
		lines[y] = applyGlobeOverlay(lines[y], overlay[y], theme)
	}

	return lines
}

func drawGeoArc(
	lat1, lon1, lat2, lon2 float64,
	age int,
	project func(float64, float64) screenPoint,
	plot func(int, int, rune),
) {
	toVector := func(lat, lon float64) [3]float64 {
		lat *= math.Pi / 180
		lon *= math.Pi / 180
		return [3]float64{
			math.Cos(lat) * math.Cos(lon),
			math.Cos(lat) * math.Sin(lon),
			math.Sin(lat),
		}
	}

	a := toVector(lat1, lon1)
	b := toVector(lat2, lon2)

	dot := a[0]*b[0] + a[1]*b[1] + a[2]*b[2]
	dot = math.Max(-1, math.Min(1, dot))
	omega := math.Acos(dot)
	sinOmega := math.Sin(omega)

	// Sample only the moving head and its two fading positions.
	for trail := 1; trail >= 0; trail-- {
		t := float64(age-trail) / float64(signalFrames-1)
		if t < 0 || t > 1 {
			continue
		}

		var v [3]float64

		if omega < 1e-8 || math.Abs(sinOmega) < 1e-8 {
			for k := range v {
				v[k] = a[k]*(1-t) + b[k]*t
			}
		} else {
			wa := math.Sin((1-t)*omega) / sinOmega
			wb := math.Sin(t*omega) / sinOmega
			for k := range v {
				v[k] = wa*a[k] + wb*b[k]
			}
		}

		length := math.Sqrt(v[0]*v[0] + v[1]*v[1] + v[2]*v[2])
		if length == 0 {
			continue
		}
		for k := range v {
			v[k] /= length
		}

		lat := math.Asin(v[2]) * 180 / math.Pi
		lon := math.Atan2(v[1], v[0]) * 180 / math.Pi
		current := project(lon, lat)
		if current.depth > 0 {
			glyph := '.'
			if trail == 0 {
				glyph = '*'
				if age%4 == 2 {
					glyph = '+'
				}
			}
			plot(current.x, current.y, glyph)
		}
	}
}

const signalFrames = 9
const maxSignals = 12

func signalLimit(width, height int) int {
	// A wide, shallow panel has empty columns on either side of the globe.
	width = min(width, int(math.Ceil(float64(height)*2*globeMaxAspect*(0.47/0.46))))
	return min(maxSignals, max(1, width*height/180))
}

func applyGlobeOverlay(line string, overlay []rune, theme Theme) string {
	var out strings.Builder
	cell := 0

	for i := 0; i < len(line); {
		if line[i] == 0x1b && i+1 < len(line) && line[i+1] == '[' {
			start := i
			i += 2
			for i < len(line) {
				c := line[i]
				i++
				if c >= 0x40 && c <= 0x7e {
					break
				}
			}
			out.WriteString(line[start:i])
			continue
		}

		_, size := decodeRune(line[i:])
		if size == 0 {
			break
		}

		if cell < len(overlay) && overlay[cell] != 0 && overlay[cell] != ' ' {
			char := overlay[cell]
			color := theme.Data
			switch char {
			case '.':
				color = theme.Muted
			case '*', '+':
				color = theme.Accent
			case 'S':
				color = theme.Data
			}
			if theme.Traffic != "" {
				color = theme.Traffic
				if char == '*' || char == 'S' { color += "\033[1m" }
			}
			out.WriteString(color)
			out.WriteRune(char)
			out.WriteString("\033[0m")
		} else {
			out.WriteString(line[i : i+size])
		}

		cell++
		i += size
	}
	return out.String()
}

func decodeRune(s string) (rune, int) {
	for _, r := range s {
		return r, len(string(r))
	}
	return 0, 0
}

func geoStatus(options GeoOptions, count int) string {
	if options.Resolver == nil {
		return "GEOIP: OFF"
	}
	return fmt.Sprintf("GEOIP: ON | GEO POINTS: %d", count)
}
