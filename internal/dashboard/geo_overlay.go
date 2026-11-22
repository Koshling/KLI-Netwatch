package dashboard

import (
	"fmt"
	"math"
	"strings"

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
	Count     int
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
) []string {
	lines := renderGlobe(width, height, angle, theme)
	if width < 2 || height < 2 || (server == nil && len(visitors) == 0) {
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
		if old == 'S' || old == '@' {
			return
		}
		if char == 'S' || char == '@' || old == 0 || old == ' ' || char == '*' {
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

	if server != nil {
		for _, visitor := range visitors {
			drawGeoArc(
				server.Latitude, server.Longitude,
				visitor.Latitude, visitor.Longitude,
				project, plot,
			)
		}
	}

	for _, visitor := range visitors {
		p := project(visitor.Longitude, visitor.Latitude)
		if p.depth <= 0 {
			continue
		}
		marker := '*'
		if visitor.Count > 1 {
			marker = '@'
		}
		plot(p.x, p.y, marker)
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

	const steps = 96
	var previous screenPoint
	previousVisible := false

	for i := 0; i <= steps; i++ {
		t := float64(i) / steps
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
		visible := current.depth > 0

		if visible && previousVisible {
			drawScreenLine(previous.x, previous.y, current.x, current.y, plot)
		}
		previous = current
		previousVisible = visible
	}
}

func drawScreenLine(x0, y0, x1, y1 int, plot func(int, int, rune)) {
	dx := int(math.Abs(float64(x1 - x0)))
	dy := int(math.Abs(float64(y1 - y0)))
	sx, sy := -1, -1
	if x0 < x1 {
		sx = 1
	}
	if y0 < y1 {
		sy = 1
	}

	err := dx - dy
	for {
		plot(x0, y0, '.')
		if x0 == x1 && y0 == y1 {
			break
		}
		e2 := 2 * err
		if e2 > -dy {
			err -= dy
			x0 += sx
		}
		if e2 < dx {
			err += dx
			y0 += sy
		}
	}
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
			case '*', '@':
				color = theme.Accent
			case 'S':
				color = theme.Data
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
