package bankimport

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
	textunicode "golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

// CSV reads a bounded bank statement locally, preserving repeated transactions.
func CSV(ctx context.Context, data []byte, options TableOptions) ([]store.BankTransaction, error) {
	o, err := options.normalized()
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > MaxFileBytes {
		return nil, ErrFile
	}
	text, err := decodeCSV(data, o.Encoding)
	if err != nil {
		return nil, err
	}
	delimiters := []rune{';', ',', '\t'}
	switch o.Delimiter {
	case "semicolon":
		delimiters = []rune{';'}
	case "comma":
		delimiters = []rune{','}
	case "tab":
		delimiters = []rune{'\t'}
	}
	if first, rest, found := strings.Cut(text, "\n"); found && strings.HasPrefix(strings.ToLower(first), "sep=") {
		value := strings.TrimSuffix(first[4:], "\r")
		if value != ";" && value != "," && value != "\t" {
			return nil, tableError("csv", 1)
		}
		declared := []rune(value)[0]
		if o.Delimiter != "auto" && delimiters[0] != declared {
			return nil, tableError("options", 1)
		}
		delimiters = []rune{declared}
		text = rest
		// HeaderRow counts records after an optional Excel-style separator directive.
	}
	var chosen *table
	var parseErr error
	for _, delimiter := range delimiters {
		candidate, err := readCSV(ctx, text, delimiter)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			parseErr = err
			continue
		}
		if _, _, err := locateHeader(candidate, o); err != nil {
			continue
		}
		if chosen != nil {
			return nil, tableError("mapping", 0)
		}
		chosen = &candidate
	}
	if chosen == nil {
		if len(delimiters) == 1 && parseErr != nil {
			return nil, parseErr
		}
		return nil, tableError("mapping", 0)
	}
	return parseTable(ctx, *chosen, o)
}

func decodeCSV(data []byte, encoding string) (string, error) {
	var decoded []byte
	var err error
	switch {
	case encoding == "auto" && (bytes.HasPrefix(data, []byte{0xff, 0xfe}) || bytes.HasPrefix(data, []byte{0xfe, 0xff})):
		decoded, _, err = transform.Bytes(textunicode.UTF16(textunicode.LittleEndian, textunicode.ExpectBOM).NewDecoder(), data)
	case encoding == "windows1252" || (encoding == "auto" && !utf8.Valid(data)):
		decoded, err = charmap.Windows1252.NewDecoder().Bytes(data)
	default:
		if !utf8.Valid(data) {
			return "", tableError("encoding", 0)
		}
		decoded = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	}
	if err != nil || len(decoded) > MaxFileBytes || bytes.ContainsRune(decoded, utf8.RuneError) || bytes.ContainsRune(decoded, 0) {
		return "", tableError("encoding", 0)
	}
	return string(decoded), nil
}

func readCSV(ctx context.Context, text string, delimiter rune) (table, error) {
	reader := csv.NewReader(strings.NewReader(text))
	reader.Comma = delimiter
	reader.FieldsPerRecord = -1
	out := table{strictWidth: true}
	size := 0
	for {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return out, tableError("csv", len(out.rows)+1)
		}
		line, _ := reader.FieldPos(0)
		if len(out.rows) >= maxTableRows || len(record) > maxColumns {
			return out, tableError("limit", line)
		}
		row := tableRow{number: line, cells: make([]tableCell, len(record))}
		for i, value := range record {
			size += len(value)
			if len(value) > maxCellBytes || size > maxTableBytes {
				return out, tableError("limit", line)
			}
			row.cells[i].text = strings.TrimSpace(value)
		}
		out.rows = append(out.rows, row)
	}
}
