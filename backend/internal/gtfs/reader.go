package gtfs

import (
	"archive/zip"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/bhumika1331/namma-transit/backend/internal/domain"
)

// row is one CSV record with header-name access.
type row struct {
	fields []string
	idx    map[string]int
}

func (r row) get(col string) string {
	i, ok := r.idx[col]
	if !ok || i >= len(r.fields) {
		return ""
	}
	return r.fields[i]
}

func (r row) has(col string) bool {
	_, ok := r.idx[col]
	return ok
}

// forEachRow streams a CSV file inside the zip, calling fn per data row.
// Missing optional files return errNoFile so callers can skip them.
func forEachRow(z *zip.ReadCloser, name string, fn func(row) error) error {
	var f *zip.File
	for _, cand := range z.File {
		if cand.Name == name {
			f = cand
			break
		}
	}
	if f == nil {
		return errNoFile
	}
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("open %s: %w", name, err)
	}
	defer rc.Close()

	cr := csv.NewReader(rc)
	cr.ReuseRecord = true
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true

	header, err := cr.Read()
	if err != nil {
		return fmt.Errorf("read %s header: %w", name, err)
	}
	idx := make(map[string]int, len(header))
	for i, h := range header {
		idx[strings.TrimPrefix(strings.TrimSpace(h), "\uFEFF")] = i
	}
	for line := 2; ; line++ {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%s line %d: %w", name, line, err)
		}
		if err := fn(row{fields: rec, idx: idx}); err != nil {
			return fmt.Errorf("%s line %d: %w", name, line, err)
		}
	}
}

var errNoFile = errors.New("file not in feed")

// parseTime converts GTFS "HH:MM:SS" (hours may exceed 23) to seconds.
func parseTime(s string) (domain.Seconds, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty time")
	}
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("bad time %q", s)
	}
	var v [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return 0, fmt.Errorf("bad time %q", s)
		}
		v[i] = n
	}
	return domain.Seconds(v[0]*3600 + v[1]*60 + v[2]), nil
}

func parseFloat(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	return f, err == nil
}
