package dashboard

type Theme struct {
	Name       string
	UI         string
	Data       string
	Accent     string
	Muted      string
	Background string
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
		UI:         "\033[37m",
		Data:       "\033[32m",
		Accent:     "\033[1;92m",
		Muted:      "\033[2;32m",
		Background: "\033[40m",
	},
	{
		Name:       "TERMINAL",
		UI:         "\033[32m",
		Data:       "\033[32m",
		Accent:     "\033[1;92m",
		Muted:      "\033[2;32m",
		Background: "\033[40m",
	},
	{
		Name:       "WESTERN",
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
}
