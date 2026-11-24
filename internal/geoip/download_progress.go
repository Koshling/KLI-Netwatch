package geoip

import (
	"fmt"
	"io"
	"strings"
)

// Report milestones on separate lines so logs and terminals show the same progress.
type archiveProgress struct {
	source   io.Reader
	out      io.Writer
	total    int64
	read     int64
	lastStep int64
}

func (p *archiveProgress) Read(buf []byte) (int, error) {
	n, err := p.source.Read(buf)
	p.read += int64(n)
	if p.out != nil && p.step() > p.lastStep {
		p.report()
	}
	return n, err
}

func (p *archiveProgress) step() int64 {
	if p.total > 0 {
		return min(4, int64(4*float64(p.read)/float64(p.total)))
	}
	return p.read / (4 << 20)
}

func (p *archiveProgress) report() {
	p.lastStep = p.step()
	if p.total > 0 {
		filled := int(p.lastStep)
		percent := min(100, int(100*float64(p.read)/float64(p.total)))
		fmt.Fprintf(p.out, "Downloading GeoIP [%s%s] %d%%\n", strings.Repeat("##", filled), strings.Repeat("..", 4-filled), percent)
		return
	}
	fmt.Fprintf(p.out, "Downloading GeoIP: %.1f MiB received\n", float64(p.read)/(1<<20))
}
