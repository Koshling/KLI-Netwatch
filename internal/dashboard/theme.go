package dashboard

type Theme struct {
	Name       string
	Traffic    string
	UserPulse  string
	Heading    string
	UI         string
	Data       string
	Accent     string
	Muted      string
	Background string
}

func (t Theme) heading() string {
	if t.Heading != "" {
		return t.Heading
	}
	return t.Accent
}

var themes = []Theme{
	{
		Name:       "MONOCHROME",
		UI:         "\033[37m",
		Data:       "\033[97m",
		Accent:     "\033[1;97m",
		Muted:      "\033[90m",
		Background: "\033[40m",
	},
	{
		Name:       "MATRIX",
		Heading:    "\033[1;97m",
		Traffic:    "\033[96m",
		UI:         "\033[37m",
		Data:       "\033[32m",
		Accent:     "\033[1;92m",
		Muted:      "\033[2;32m",
		Background: "\033[40m",
	},
	{
		Name:       "TERMINAL",
		Traffic:    "\033[97m",
		UI:         "\033[32m",
		Data:       "\033[32m",
		Accent:     "\033[1;92m",
		Muted:      "\033[2;32m",
		Background: "\033[40m",
	},
	{
		Name:       "WESTERN",
		Traffic:    "\033[38;5;68m",
		UI:         "\033[37m",
		Data:       "\033[33m",
		Accent:     "\033[1;93m",
		Muted:      "\033[2;33m",
		Background: "\033[40m",
	},
	{
		Name:       "REVERSE MONO",
		UI:         "\033[30;47m",
		Data:       "\033[30;47m",
		Accent:     "\033[1;30;47m",
		Muted:      "\033[2;30;47m",
		Background: "\033[47m",
	},
	{
		Name:       "ICE",
		Traffic:    "\033[97m",
		UserPulse:  "\033[1;97m",
		Heading:    "\033[1;97m",
		UI:         "\033[97m",
		Data:       "\033[38;5;75m",
		Accent:     "\033[1;38;5;75m",
		Muted:      "\033[38;5;67m",
		Background: "\033[40m",
	},
	{
		Name:       "AMBER",
		Traffic:    "\033[38;5;230m",
		UserPulse:  "\033[1;38;5;230m",
		UI:         "\033[38;5;214m",
		Data:       "\033[38;5;214m",
		Accent:     "\033[1;38;5;220m",
		Muted:      "\033[38;5;172m",
		Background: "\033[40m",
	},
}
