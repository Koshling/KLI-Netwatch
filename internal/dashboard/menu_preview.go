package dashboard

import (
	"math"
	"strings"
)

func menuPreview(mode Mode, icon string, width, height, frame int, t Theme) []string {
	if height < 3 || (mode == convert && width < 13) {
		rows := make([]string, height)
		if height > 0 {
			padding := max(0, (width-len([]rune(icon)))/2)
			rows[height/2] = fitANSI(t.Data+strings.Repeat(" ", padding)+icon, width)
			for y, row := range rows {
				if row == "" {
					rows[y] = t.Muted + string(previewGrid(width, y))
				}
			}
		}
		return rows
	}

	switch mode {
	case Live:
		// Reuse the real globe. Keep its proportions on tall, narrow cards.
		globeHeight := min(height, width/2)
		globe := renderGlobe(width, globeHeight, float64(frame%80)*2*math.Pi/80, t)
		top := (height - globeHeight) / 2
		rows := make([]string, height)
		for y := range rows {
			if y >= top && y < top+globeHeight {
				rows[y] = globe[y-top]
			} else {
				rows[y] = t.Muted + string(previewGrid(width, y))
			}
		}
		return rows
	case Interval:
		return previewMap(width, height, frame, t)
	case Replay:
		return previewReplay(width, height, frame, t)
	default:
		return previewConversion(width, height, frame, t)
	}
}

func previewGrid(width, y int) []rune {
	row := []rune(strings.Repeat(" ", width))
	for x := 2 + (y%2)*3; x < width-1; x += 6 {
		row[x] = '·'
	}
	return row
}

func previewMap(width, height, frame int, t Theme) []string {
	landMaskOnce.Do(buildLandMask)
	rows := make([]string, height)
	// A 2:1 world map is about four character cells wide per row.
	mapHeight := min(height, max(3, width/4))
	top := (height - mapHeight) / 2
	sweep := frame % mapHeight
	for y := range rows {
		row := previewGrid(width, y)
		color := t.Muted
		if y >= top && y < top+mapHeight {
			color = t.Data
			for x := range row {
				lon := (float64(x)+0.5)*360/float64(width) - 180
				lat := 90 - (float64(y-top)+0.5)*180/float64(mapHeight)
				if isLand(lon, lat) {
					row[x] = '▒'
					if y-top == sweep {
						row[x] = '█'
					}
				}
			}
			if y-top == sweep {
				color = t.Accent
			}
		}
		rows[y] = color + string(row)
	}
	return rows
}

func previewReplay(width, height, frame int, t Theme) []string {
	rows := make([]string, height)
	// Scanline and deterministic interference stay behind the playback icon.
	scan := frame % height
	for y := range rows {
		row := previewGrid(width, y)
		color := t.Muted
		if y == scan || y == (scan+height/2)%height {
			for x := (frame + y) % 4; x < width; x += 4 {
				row[x] = '░'
				if (x+y+frame)%3 == 0 {
					row[x] = '─'
				}
			}
		}
		middle := height / 2
		if y == middle-1 {
			stampPreview(row, " ╭─────╮ ")
		}
		if y == middle {
			spinner := [...]string{"◴", "◷", "◶", "◵"}
			play := "▶"
			if frame%6 >= 4 {
				play = "▷"
			}
			stampPreview(row, "│"+spinner[frame%len(spinner)]+" "+play+"  │")
			color = t.Accent
		}
		if y == middle+1 {
			stampPreview(row, " ╰─────╯ ")
		}
		rows[y] = color + string(row)
	}
	return rows
}

func previewConversion(width, height, frame int, t Theme) []string {
	rows := make([]string, height)
	transfer := [...]string{"·─→", "─→·", "→·─"}
	icon := []string{"╭───╮   ╭───╮", "│a,b│" + transfer[frame%len(transfer)] + "│010│", "╰───╯   ╰───╯"}
	if width >= 25 && height >= 5 {
		icon = []string{
			"╭──────╮       ╭──────╮",
			"│ CSV  │       │NWGEO │",
			"│ a,b  │  " + transfer[frame%len(transfer)] + "  │ 0101 │",
			"│ c,d  │       │ 1100 │",
			"╰──────╯       ╰──────╯",
		}
	}
	top := (height - len(icon)) / 2
	for y := range rows {
		row := previewGrid(width, y)
		color := t.Muted
		if y >= top && y < top+len(icon) {
			stampPreview(row, icon[y-top])
			color = t.Data
		}
		rows[y] = color + string(row)
	}
	return rows
}

func stampPreview(row []rune, art string) {
	glyphs := []rune(art)
	copy(row[max(0, (len(row)-len(glyphs))/2):], glyphs)
}
