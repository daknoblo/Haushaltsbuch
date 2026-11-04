package bankimport

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"math"
	"path"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

const maxWorkbookBytes = 32 << 20

// XLSX reads cell values without evaluating formulas or following external links.
func XLSX(ctx context.Context, data []byte, options TableOptions) ([]store.BankTransaction, error) {
	o, err := options.normalized()
	if err != nil {
		return nil, err
	}
	if err := validateWorkbook(ctx, data); err != nil {
		return nil, err
	}
	workbook, err := excelize.OpenReader(bytes.NewReader(data), excelize.Options{
		RawCellValue: true, UnzipSizeLimit: maxWorkbookBytes, UnzipXMLSizeLimit: maxWorkbookBytes,
	})
	if err != nil {
		return nil, tableError("xlsx", 0)
	}
	defer func() { _ = workbook.Close() }()
	sheets := workbook.GetSheetList()
	sheet := o.Sheet
	if sheet == "" {
		if len(sheets) != 1 {
			return nil, ErrSheet
		}
		sheet = sheets[0]
	}
	found := false
	for _, name := range sheets {
		if name == sheet {
			found = true
		}
	}
	if !found {
		return nil, ErrSheet
	}
	properties, err := workbook.GetWorkbookProps()
	if err != nil {
		return nil, tableError("xlsx", 0)
	}
	out := table{date1904: properties.Date1904 != nil && *properties.Date1904}
	rows, err := workbook.Rows(sheet)
	if err != nil {
		return nil, tableError("xlsx", 0)
	}
	defer func() { _ = rows.Close() }()
	size := 0
	for number := 1; rows.Next(); number++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if number > maxTableRows {
			return nil, tableError("limit", number)
		}
		values, err := rows.Columns(excelize.Options{RawCellValue: true})
		if err != nil {
			return nil, tableError("xlsx", number)
		}
		if len(values) > maxColumns {
			return nil, tableError("limit", number)
		}
		row := tableRow{number: number, cells: make([]tableCell, len(values))}
		for col, value := range values {
			size += len(value)
			if len(value) > maxCellBytes || size > maxTableBytes {
				return nil, tableError("limit", number)
			}
			address, err := excelize.CoordinatesToCellName(col+1, number)
			if err != nil {
				return nil, tableError("xlsx", number)
			}
			kind, err := workbook.GetCellType(sheet, address)
			if err != nil {
				return nil, tableError("xlsx", number)
			}
			formula, err := workbook.GetCellFormula(sheet, address)
			if err != nil {
				return nil, tableError("xlsx", number)
			}
			row.cells[col] = tableCell{text: strings.TrimSpace(value),
				numeric: kind == excelize.CellTypeNumber || (kind == excelize.CellTypeUnset && value != ""), formula: formula != ""}
		}
		out.rows = append(out.rows, row)
	}
	if rows.Error() != nil {
		return nil, tableError("xlsx", 0)
	}
	return parseTable(ctx, out, o)
}

func excelDate(s string, date1904 bool) (string, error) {
	number, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 || (number == 0 && !date1904) || number > 2958465 ||
		number != math.Trunc(number) || (!date1904 && number == 60) {
		return "", ErrResult
	}
	date, err := excelize.ExcelDateToTime(number, date1904)
	if err != nil {
		return "", ErrResult
	}
	return date.Format("2006-01-02"), nil
}

func validateWorkbook(ctx context.Context, data []byte) error {
	if len(data) == 0 || len(data) > MaxFileBytes {
		return ErrFile
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(archive.File) > 256 {
		return tableError("xlsx", 0)
	}
	var total uint64
	seen := make(map[string]bool, len(archive.File))
	for _, file := range archive.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		if file.UncompressedSize64 > maxWorkbookBytes || total > maxWorkbookBytes-file.UncompressedSize64 {
			return tableError("limit", 0)
		}
		total += file.UncompressedSize64
		cleanName := strings.TrimSuffix(file.Name, "/")
		if cleanName == "" || path.IsAbs(cleanName) || path.Clean(cleanName) != cleanName ||
			strings.Contains(cleanName, "\\") || strings.HasPrefix(cleanName, "../") || cleanName == ".." || seen[cleanName] {
			return tableError("xlsx", 0)
		}
		seen[cleanName] = true
		name := strings.ToLower(file.Name)
		if strings.Contains(name, "vbaproject") {
			return tableError("xlsx", 0)
		}
		if strings.HasPrefix(name, "xl/worksheets/") && strings.HasSuffix(name, ".xml") {
			if err := validateWorksheet(ctx, file); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateWorksheet(ctx context.Context, file *zip.File) error {
	reader, err := file.Open()
	if err != nil {
		return tableError("xlsx", 0)
	}
	defer func() { _ = reader.Close() }()
	decoder := xml.NewDecoder(io.LimitReader(reader, maxWorkbookBytes+1))
	cells, rows := 0, 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return tableError("xlsx", 0)
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "row":
			rows++
			for _, attr := range start.Attr {
				if attr.Name.Local == "r" {
					n, err := strconv.Atoi(attr.Value)
					if err != nil || n < 1 || n > maxTableRows {
						return tableError("limit", 0)
					}
				}
			}
		case "c":
			cells++
			for _, attr := range start.Attr {
				if attr.Name.Local == "r" {
					col, row, err := excelize.CellNameToCoordinates(attr.Value)
					if err != nil || col > maxColumns || row > maxTableRows {
						return tableError("limit", 0)
					}
				}
			}
		case "f":
			return tableError("formula", 0)
		}
		if rows > maxTableRows || cells > maxTableRows*maxColumns {
			return tableError("limit", 0)
		}
	}
}
