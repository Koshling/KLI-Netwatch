package dashboard

type Layout struct {
	Width       int
	Height      int
	LeftWidth   int
	RightWidth  int
	ContentRows int
	GlobeWidth  int
	GlobeHeight int
	FeedRows    int
	GraphHeight int
	StatusRows  int
}

func calculateLayout(width, height int) Layout {
	if width < 10 {
		width = 10
	}
	if height < 8 {
		height = 8
	}

	l := Layout{
		Width:       width,
		Height:      height,
		ContentRows: height - 5,
		StatusRows:  3,
	}

	if width < 120 || height < 30 {
		l.StatusRows = 2
	}

	available := width - 3
	l.LeftWidth = available / 2
	l.RightWidth = available - l.LeftWidth

	l.GlobeWidth = l.LeftWidth - 2
	if l.GlobeWidth < 1 {
		l.GlobeWidth = 1
	}

	l.GlobeHeight = l.ContentRows - 3
	maxGlobeHeight := int(float64(l.GlobeWidth) / 2.32)
	if l.GlobeHeight > maxGlobeHeight {
		l.GlobeHeight = maxGlobeHeight
	}
	if l.GlobeHeight < 1 {
		l.GlobeHeight = 1
	}

	fixedRows := 6 + l.StatusRows
	availableRows := l.ContentRows - fixedRows
	if availableRows < 0 {
		availableRows = 0
	}

	l.GraphHeight = availableRows / 2
	l.FeedRows = availableRows - l.GraphHeight

	return l
}
