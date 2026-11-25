package geoip

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
)

type contextReader struct {
	ctx    context.Context
	source io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.source.Read(p)
}

const maxCSVRecordBytes = 256 << 10

// csvInput limits bytes consumed during a single CSV read, including quoted
// multiline fields. The CSV decoder may prefetch bytes for the next record.
type csvInput struct {
	source io.Reader
	read   int64
	limit  int64
}

func (r *csvInput) Read(p []byte) (int, error) {
	if r.read >= r.limit {
		return 0, fmt.Errorf("CSV record exceeds %d bytes", maxCSVRecordBytes)
	}
	n, err := r.source.Read(p[:min(int64(len(p)), r.limit-r.read)])
	r.read += int64(n)
	return n, err
}

type boundedCSV struct {
	input *csvInput
	csv   *csv.Reader
}

func newBoundedCSV(src io.Reader, fields int) *boundedCSV {
	input := &csvInput{source: src}
	reader := csv.NewReader(input)
	reader.FieldsPerRecord = fields
	reader.ReuseRecord = true
	return &boundedCSV{input: input, csv: reader}
}

func (r *boundedCSV) Read() ([]string, error) {
	offset := r.csv.InputOffset()
	r.input.limit = offset + maxCSVRecordBytes + 1
	record, err := r.csv.Read()
	if r.csv.InputOffset()-offset > maxCSVRecordBytes {
		return nil, fmt.Errorf("CSV record exceeds %d bytes", maxCSVRecordBytes)
	}
	return record, err
}
