package dashboard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"netwatch/internal/geoip"
)

type Mode string

const (
	Live     Mode = "live"
	Replay   Mode = "replay"
	Interval Mode = "interval"
	convert  Mode = "geoip"
)

const DefaultHistoryDuration = 15 * time.Minute

var errHistoryTimestamps = errors.New("supply both --from and --to in RFC3339 without --last")

// LaunchOptions describes the same mode and log bounds for CLI and menu launches.
type LaunchOptions struct {
	Mode     Mode
	LogPath  string
	Last     time.Duration
	From, To string
}

func (o LaunchOptions) HistoryBounds(now time.Time) (time.Time, time.Time, error) {
	start, end := now.Add(-o.Last), now
	if o.From != "" || o.To != "" {
		var fromErr, toErr error
		start, fromErr = time.Parse(time.RFC3339, o.From)
		end, toErr = time.Parse(time.RFC3339, o.To)
		if fromErr != nil || toErr != nil {
			return time.Time{}, time.Time{}, errHistoryTimestamps
		}
	}
	if !start.Before(end) {
		return time.Time{}, time.Time{}, errors.New("replay interval must have positive duration")
	}
	return start, end, nil
}

var modeCards = [...]struct {
	mode        Mode
	title       string
	art         string
	description string
}{
	{Live, "LIVE", "─╮╭─╮╭─", "Follow new requests"},
	{Replay, "REPLAY", "◀ ─▶─ ▶", "Play historical requests"},
	{Interval, "INTERVAL", "▂▅▃▇▅▂▆", "Summarize a time range"},
	{convert, "GEOIP CONVERT", "CSV → NWGEO", "Convert a local GeoIP CSV"},
}

// Menu returns when a mode is selected, or returns false on quit.
func (s *Session) Menu(ctx context.Context, previous LaunchOptions, problem error) (LaunchOptions, bool, error) {
	s.open()
	var view screen
	selected := 0
	for i, card := range modeCards {
		if previous.Mode == card.mode {
			selected = i
			break
		}
	}
	note := ""
	if problem != nil {
		note = problem.Error()
	}
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	frame := 0
	for ctx.Err() == nil {
		width, height := terminalSize()
		t := themes[s.themeIndex]
		columns := menuColumns(width, height)
		rows := menuRows(width, height, columns, selected, frame, note, t)
		if err := view.writeFrame(rows, Layout{Width: width, Height: height}, t); err != nil {
			return LaunchOptions{}, false, err
		}

		select {
		case <-ctx.Done():
		case <-tick.C:
			frame++
		case key, ok := <-s.keys:
			if !ok || key == 'q' || key == 'Q' {
				return LaunchOptions{}, false, nil
			}
			mode := modeCards[selected].mode
			switch key {
			case keyUp, keyDown, keyLeft, keyRight:
				selected = moveModeSelection(selected, columns, key)
			case '1', '2', '3', '4', '5':
				s.selectTheme(key)
				view.clearScreen = true
			case '\r', '\n':
				if mode != convert {
					return LaunchOptions{Mode: mode, Last: DefaultHistoryDuration}, true, nil
				}
				message, err := s.convertGeoIP(ctx)
				if err != nil {
					return LaunchOptions{}, false, err
				}
				if message != "" {
					note = message
				}
				view = screen{}
			case 's', 'S':
				if mode == Replay || mode == Interval {
					options, accepted, err := s.configureHistory(ctx, mode, previous)
					if accepted || err != nil {
						return options, accepted, err
					}
					view = screen{}
				}
			}
		}
	}
	return LaunchOptions{}, false, nil
}

func menuColumns(width, height int) int {
	switch {
	case width >= 76 && width < 140 && height >= 22:
		return 2
	case width >= 76 && height >= 8:
		return 4
	case width >= 38 && height >= 13:
		return 2
	default:
		return 1
	}
}

func moveModeSelection(selected, columns int, key rune) int {
	step := 1
	if columns == 2 && (key == keyUp || key == keyDown) {
		step = columns
	}
	if key == keyUp || key == keyLeft {
		step = -step
	}
	return (selected + step + len(modeCards)) % len(modeCards)
}

func menuRows(width, height, columns, selected, frame int, note string, t Theme) []string {
	if note == "" {
		note = modeCards[selected].description
	}
	footer := "[Enter] Open  [S] History settings  [Q] Exit"
	if width >= 61 {
		footer = "[Arrows] Select  " + footer
	} else if width < 45 {
		footer = "↑↓ Enter  S:Settings  Q:Exit"
		if width < 28 {
			footer = "↑↓ / Enter / S / Q"
		}
	}
	if columns == 1 {
		var items []string
		for i, card := range modeCards {
			items = append(items, menuChoice(card.title, i == selected, t))
		}
		return menuListRows(width, height, "MENU", items, selected, note, footer, t)
	}

	rows := make([]string, height)
	rows[0] = fitANSI(t.Accent+"MENU", width)
	rows[1] = fitANSI(t.Muted+strings.Repeat("═", width), width)
	const gap = 2
	groups := len(modeCards) / columns
	gridHeight := height - 4 - (groups - 1)
	gridWidth := width - (columns-1)*gap
	for index, card := range modeCards {
		border := t.Muted
		title := "  " + card.title
		if index == selected {
			border = t.Accent
			title = "> " + card.title
		}
		group, column := index/columns, index%columns
		top := 2 + group + group*gridHeight/groups
		cardHeight := (group+1)*gridHeight/groups - group*gridHeight/groups
		cardWidth := (column+1)*gridWidth/columns - column*gridWidth/columns

		space := ""
		if column != 0 {
			space = strings.Repeat(" ", gap)
		}
		line := strings.Repeat("─", cardWidth-2)
		rows[top] += space + border + "┌" + line + "┐"
		rows[top+1] += space + border + "│" + fitANSI(title, cardWidth-2) + "│"
		for i, preview := range menuPreview(card.mode, card.art, cardWidth-2, cardHeight-3, frame, t) {
			rows[top+2+i] += space + border + "│" + preview + border + "│"
		}
		rows[top+cardHeight-1] += space + border + "└" + line + "┘"
	}
	rows[height-2] = fitANSI(t.UI+printableField(note), width)
	rows[height-1] = fitANSI(t.Accent+footer, width)
	return rows
}

func menuChoice(label string, selected bool, t Theme) string {
	if selected {
		return t.Accent + "> " + label
	}
	return t.UI + "  " + label
}

// Keep the focused row visible even when only one option fits in the terminal.
func menuListRows(width, height int, title string, items []string, selected int, note, footer string, t Theme) []string {
	rows := make([]string, height)
	if height == 1 {
		rows[0] = fitANSI(items[selected], width)
		return rows
	}
	rows[0] = fitANSI(t.Accent+title, width)
	first, end := 1, height
	if height >= 3 {
		end--
		rows[end] = fitANSI(t.Accent+footer, width)
	}
	if height >= 4 && note != "" {
		end--
		rows[end] = fitANSI(t.UI+printableField(note), width)
	}
	if height >= 6 {
		rows[first] = fitANSI(t.Muted+strings.Repeat("═", width), width)
		first++
	}
	visible := end - first
	offset := min(max(0, selected-visible+1), max(0, len(items)-visible))
	for i := 0; i < visible && offset+i < len(items); i++ {
		rows[first+i] = fitANSI(items[offset+i], width)
	}
	return rows
}

func (s *Session) configureHistory(ctx context.Context, mode Mode, previous LaunchOptions) (LaunchOptions, bool, error) {
	options := LaunchOptions{Mode: mode, Last: DefaultHistoryDuration}
	rangeText, logPath := DefaultHistoryDuration.String(), ""
	if previous.Mode == mode {
		logPath = previous.LogPath
		if previous.From != "" || previous.To != "" {
			rangeText = previous.From + "/" + previous.To
		} else if previous.Last != DefaultHistoryDuration {
			rangeText = previous.Last.String()
		}
	}
	fields := []menuField{{label: "Time range", value: rangeText}, {label: "Log file", value: logPath}}
	accepted, err := s.runForm(ctx, strings.ToUpper(string(mode))+" SETTINGS", "Start", fields,
		"Duration (15m, 1h) or RFC3339 start/end. Blank log uses access_log.", "", func() error {
			options.LogPath = fields[1].value
			value := strings.TrimSpace(fields[0].value)
			options.From, options.To = "", ""
			if from, to, ok := strings.Cut(value, "/"); ok {
				options.From, options.To = strings.TrimSpace(from), strings.TrimSpace(to)
				if options.From == "" || options.To == "" {
					return errors.New("enter both RFC3339 timestamps as start/end")
				}
			} else {
				duration, err := time.ParseDuration(value)
				if err != nil || duration <= 0 {
					return errors.New("enter a positive duration or RFC3339 start/end")
				}
				options.Last = duration
			}
			_, _, err := options.HistoryBounds(time.Now())
			if errors.Is(err, errHistoryTimestamps) {
				return errors.New("enter both RFC3339 timestamps as start/end")
			}
			return err
		})
	return options, accepted, err
}

func (s *Session) convertGeoIP(ctx context.Context) (string, error) {
	fields := []menuField{
		{label: "Source CSV"},
		{label: "Output"},
		{label: "IP family", choices: []string{"IPv4", "IPv6"}},
	}
	var destination string
	accepted, err := s.runForm(ctx, "GEOIP CONVERT", "Convert", fields,
		"Use an existing folder or end a new folder path with /.", "Converting CSV...", func() error {
			source, output := fields[0].value, fields[1].value
			if source == "" || output == "" {
				return errors.New("enter the CSV path and output file or folder")
			}
			family := 4
			if fields[2].choice == 1 {
				family = 6
			}
			destination = geoIPDestination(output, family)
			return geoip.ConvertCSVContext(ctx, source, destination, family)
		})
	if !accepted || err != nil {
		return "", err
	}
	return "Created " + destination, nil
}

func geoIPDestination(path string, family int) string {
	info, err := os.Stat(path)
	if strings.HasSuffix(path, "/") || (err == nil && info.IsDir()) {
		return filepath.Join(path, fmt.Sprintf("ipv%d.nwgeo", family))
	}
	return path
}
