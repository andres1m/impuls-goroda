package scenic

import (
	"bufio"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"

	"github.com/uber/h3-go/v4"
)

var layerHeader = []string{"cell", "share"}

// WriteLayer writes the shares as CSV sorted by cell, so the same map always gives the same file.
func WriteLayer(w io.Writer, shares Shares) error {
	cells := make([]h3.Cell, 0, len(shares))
	for c := range shares {
		cells = append(cells, c)
	}
	slices.Sort(cells)
	out := csv.NewWriter(w)
	if err := out.Write(layerHeader); err != nil {
		return err
	}
	for _, c := range cells {
		if err := out.Write([]string{c.String(), strconv.FormatFloat(shares[c], 'f', 4, 64)}); err != nil {
			return err
		}
	}
	out.Flush()
	return out.Error()
}

func ReadLayer(r io.Reader) (Shares, error) {
	in := csv.NewReader(bufio.NewReader(r))
	in.FieldsPerRecord = len(layerHeader)
	in.ReuseRecord = true
	header, err := in.Read()
	if err != nil {
		return nil, err
	}
	if !slices.Equal(header, layerHeader) {
		return nil, errors.New("layer header is not cell,share")
	}
	shares := Shares{}
	for {
		row, err := in.Read()
		if errors.Is(err, io.EOF) {
			return shares, nil
		}
		if err != nil {
			return nil, err
		}
		cell := h3.CellFromString(row[0])
		share, err := strconv.ParseFloat(row[1], 64)
		if !cell.IsValid() || cell.Resolution() != CellResolution || err != nil || !(share > 0 && share <= 1) {
			return nil, fmt.Errorf("layer row %q is invalid", row)
		}
		shares[cell] = share
	}
}
