package dashboard

import "testing"

func TestCalculateLayout(t *testing.T) {
	sizes := [][2]int{
		{80, 24},
		{100, 30},
		{120, 30},
		{160, 40},
		{200, 50},
	}

	for _, size := range sizes {
		l := calculateLayout(size[0], size[1])

		if l.LeftWidth+l.RightWidth+3 != size[0] {
			t.Errorf("%dx%d: columns do not fit", size[0], size[1])
		}

		if l.ContentRows+5 != size[1] {
			t.Errorf("%dx%d: rows do not fit", size[0], size[1])
		}

		if l.GlobeWidth < 1 || l.GlobeHeight < 1 {
			t.Errorf("%dx%d: invalid globe dimensions", size[0], size[1])
		}

		t.Logf("%dx%d: columns %d/%d, globe %dx%d, feed %d, graph %d",
			size[0], size[1],
			l.LeftWidth, l.RightWidth,
			l.GlobeWidth, l.GlobeHeight,
			l.FeedRows, l.GraphHeight)
	}
}
