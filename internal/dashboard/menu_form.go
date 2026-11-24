package dashboard

import (
	"context"
	"time"
	"unicode"
	"unicode/utf8"
)

type menuField struct {
	label   string
	value   string
	choices []string
	choice  int
}

func (f *menuField) edit(key rune) {
	if len(f.choices) != 0 {
		switch key {
		case keyLeft:
			f.choice = (f.choice + len(f.choices) - 1) % len(f.choices)
		case keyRight, ' ':
			f.choice = (f.choice + 1) % len(f.choices)
		}
		return
	}
	switch key {
	case 8, 127:
		_, size := utf8.DecodeLastRuneInString(f.value)
		f.value = f.value[:len(f.value)-size]
	case 21: // Ctrl+U
		f.value = ""
	default:
		if unicode.IsPrint(key) {
			f.value += string(key)
		}
	}
}

func (f menuField) row(width int, selected bool, t Theme) string {
	label, value := f.label+": ", f.value
	if len(f.choices) != 0 {
		value = "< " + f.choices[f.choice] + " >"
	}
	if value == "" {
		value = "(empty)"
	}
	if width-len(label)-2 < 8 {
		label = ""
	}
	space := max(1, width-len(label)-2)
	if runes := []rune(value); selected && len(runes) > space {
		value = "…" + string(runes[len(runes)-space+1:])
	}
	return menuChoice(label+value, selected, t)
}

func (s *Session) runForm(ctx context.Context, title, action string, fields []menuField, hint, progress string, submit func() error) (bool, error) {
	var view screen
	focus, note := 0, hint
	actionRow, backRow := len(fields), len(fields)+1
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	render := func() error {
		w, h := terminalSize()
		t := themes[s.themeIndex]
		items := make([]string, 0, len(fields)+2)
		for i, field := range fields {
			items = append(items, field.row(w, i == focus, t))
		}
		items = append(items, menuChoice(action, focus == actionRow, t), menuChoice("Back", focus == backRow, t))
		footer := "Arrows  Enter  Esc:Back"
		if focus < len(fields) && len(fields[focus].choices) == 0 && w >= 39 {
			footer += "  Ctrl+U:Clear"
		}
		heading := title
		if w < 19 && focus < len(fields) {
			heading = fields[focus].label
		}
		rows := menuListRows(w, h, heading, items, focus, note, footer, t)
		return view.writeFrame(rows, Layout{Width: w, Height: h}, t)
	}
	for ctx.Err() == nil {
		if err := render(); err != nil {
			return false, err
		}
		select {
		case <-ctx.Done():
		case <-tick.C:
		case key, ok := <-s.keys:
			if !ok || key == keyEscape {
				return false, nil
			}
			switch key {
			case keyUp:
				focus = (focus + len(fields) + 1) % (len(fields) + 2)
			case keyDown, '\t':
				focus = (focus + 1) % (len(fields) + 2)
			case '\r', '\n':
				switch focus {
				case backRow:
					return false, nil
				case actionRow:
					if progress != "" {
						note = progress
						if err := render(); err != nil {
							return false, err
						}
					}
					if err := submit(); err != nil {
						note = err.Error()
					} else {
						return true, nil
					}
				default:
					focus++
				}
			default:
				if focus < len(fields) {
					fields[focus].edit(key)
					note = hint
				}
			}
		}
	}
	return false, nil
}
