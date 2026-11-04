// Package bankimport extracts statement suggestions. It never writes bookings.
package bankimport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"

	"github.com/ledongthuc/pdf"
)

// Limits bound uploaded bytes and extracted statement text independently.
const (
	MaxPDFBytes  = MaxFileBytes
	MaxTextBytes = 200_000
)

// Errors omit document content and credentials so callers can safely log them.
var (
	ErrPDF           = errors.New("bank import: unreadable, encrypted or oversized PDF")
	ErrNoText        = errors.New("bank import: PDF has no extractable text")
	ErrConfiguration = errors.New("bank import: invalid Azure identity configuration")
	ErrUpstream      = errors.New("bank import: Azure request failed")
	ErrResult        = errors.New("bank import: incomplete or invalid extraction")
	ErrDeployment    = errors.New("bank import: deployment unavailable")
)

type contextReader struct {
	ctx    context.Context
	reader io.ReaderAt
}

func (r contextReader) ReadAt(p []byte, off int64) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.ReadAt(p, off)
}

// PDFText reads text locally; it neither saves the document nor performs OCR.
func PDFText(ctx context.Context, data []byte) (string, error) {
	if len(data) < 100 || len(data) > MaxPDFBytes || !bytes.HasPrefix(data, []byte("%PDF-")) {
		return "", ErrPDF
	}
	r, err := pdf.NewReader(contextReader{ctx, bytes.NewReader(data)}, int64(len(data)))
	if err != nil {
		return "", ErrPDF
	}
	count := r.NumPage()
	if count < 1 || count > 100 {
		return "", ErrPDF
	}
	var text strings.Builder
	for page := 1; page <= count; page++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		part, err := r.Page(page).GetPlainText(nil)
		if err != nil || text.Len()+len(part)+1 > MaxTextBytes {
			return "", ErrPDF
		}
		text.WriteString(part)
		text.WriteByte('\n')
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", ErrNoText
	}
	return text.String(), nil
}
