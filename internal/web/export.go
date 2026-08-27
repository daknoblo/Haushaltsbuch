package web

import (
	"context"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/johnfercher/maroto/v2"
	"github.com/johnfercher/maroto/v2/pkg/components/text"
	"github.com/johnfercher/maroto/v2/pkg/config"
	"github.com/johnfercher/maroto/v2/pkg/consts/align"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/consts/orientation"
	"github.com/johnfercher/maroto/v2/pkg/core"
	"github.com/johnfercher/maroto/v2/pkg/props"

	"github.com/daknoblo/Haushaltsbuch/internal/calc"
	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

var pdfGrey = &props.Color{Red: 110, Green: 116, Blue: 130}

func newPDF() core.Maroto {
	cfg := config.NewBuilder().
		WithPageNumber().
		Build()
	return maroto.New(cfg)
}

// newWidePDF turns the page sideways and widens the grid, because a year has
// fifteen columns and none of them fit a portrait twelfth.
func newWidePDF(columns int) core.Maroto {
	cfg := config.NewBuilder().
		WithPageNumber().
		WithOrientation(orientation.Horizontal).
		WithMaxGridSize(columns).
		Build()
	return maroto.New(cfg)
}

func pdfHeader(ctx context.Context, m core.Maroto, title, household, subtitle string) {
	m.AddRow(12, text.NewCol(12, title, props.Text{Size: 18, Style: fontstyle.Bold}))
	m.AddRow(6, text.NewCol(12, household+"  ·  "+subtitle, props.Text{Size: 10, Color: pdfGrey}))
	m.AddRow(6, text.NewCol(12, Tf(ctx, "pdf.createdAt", time.Now().Format(T(ctx, "pdf.timeLayout"))), props.Text{Size: 8, Color: pdfGrey}))
	m.AddRow(4)
}

func pdfHeading(m core.Maroto, h string) {
	m.AddRow(10, text.NewCol(12, h, props.Text{Size: 13, Style: fontstyle.Bold, Top: 2}))
}

func pdfKV(m core.Maroto, k, v string) {
	m.AddRow(7,
		text.NewCol(8, k, props.Text{Size: 10}),
		text.NewCol(4, v, props.Text{Size: 10, Align: align.Right}),
	)
}

func pdfRow4(m core.Maroto, a, b, c, d string, bold bool) {
	style := fontstyle.Normal
	if bold {
		style = fontstyle.Bold
	}
	m.AddRow(7,
		text.NewCol(6, a, props.Text{Size: 9, Style: style}),
		text.NewCol(2, b, props.Text{Size: 9, Align: align.Right, Style: style}),
		text.NewCol(2, c, props.Text{Size: 9, Align: align.Right, Style: style}),
		text.NewCol(2, d, props.Text{Size: 9, Align: align.Right, Style: style}),
	)
}

func (s *Server) writePDF(w http.ResponseWriter, r *http.Request, m core.Maroto, filename string) {
	doc, err := m.Generate()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	// The filename embeds a user-supplied household name, so it must be encoded
	// rather than concatenated into the quoted-string form.
	w.Header().Set("Content-Disposition",
		mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	_, _ = w.Write(doc.GetBytes())
}

// pdfSlug reduces a household name to characters that are safe and readable in
// a download filename. Non-ASCII letters are kept; mime.FormatMediaType encodes
// them per RFC 2231.
func pdfSlug(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '-', r == '_':
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteRune('-')
		}
	}
	if b.Len() == 0 {
		return "haushalt"
	}
	return b.String()
}

// exportHousehold resolves the active household for an export request.
func (s *Server) exportHousehold(w http.ResponseWriter, r *http.Request) (store.Household, bool) {
	active, err := s.store.ActiveHouseholdID(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return store.Household{}, false
	}
	if active == 0 {
		s.clientError(w, r, http.StatusBadRequest, "error.noHousehold")
		return store.Household{}, false
	}
	hh, err := s.store.GetHousehold(r.Context(), active)
	if err != nil {
		s.writeStoreError(w, r, err)
		return store.Household{}, false
	}
	return hh, true
}

func (s *Server) handleExportOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	hh, ok := s.exportHousehold(w, r)
	if !ok {
		return
	}
	month := NormalizeMonth(r.URL.Query().Get("m"))
	rep, err := s.buildMonthReport(r.Context(), hh.ID, month)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	m := newPDF()
	pdfHeader(ctx, m, T(ctx, "pdf.overview"), hh.Name, MonthLabel(ctx, month))

	pdfKV(m, T(ctx, "overview.income"), FormatEUR(rep.IncomeCents))
	pdfKV(m, T(ctx, "overview.expenses"), FormatEUR(rep.ExpenseCents))
	pdfKV(m, T(ctx, "overview.balance"), FormatEUR(rep.BalanceCents))

	pdfHeading(m, T(ctx, "overview.people"))
	pdfRow4(m, T(ctx, "overview.person"), T(ctx, "overview.income"), T(ctx, "overview.expenses"), T(ctx, "overview.balance"), true)
	for _, mb := range rep.Members {
		pdfRow4(m, mb.Member.Name,
			FormatEUR(mb.IncomeCents),
			FormatEUR(mb.ExpenseCents),
			FormatEUR(mb.BalanceCents), false)
	}

	if len(rep.Categories) > 0 {
		pdfHeading(m, T(ctx, "overview.byCategory"))
		for _, c := range rep.Categories {
			pdfKV(m, c.Label, FormatEUR(c.Cents))
		}
	}

	pdfHeading(m, T(ctx, "overview.budgetClasses"))
	pdfKV(m, T(ctx, "class.need"), FormatEUR(rep.ByBudgetClass[store.ClassNeed]))
	pdfKV(m, T(ctx, "class.want"), FormatEUR(rep.ByBudgetClass[store.ClassWant]))
	pdfKV(m, T(ctx, "class.saving"), FormatEUR(rep.ByBudgetClass[store.ClassSaving]))

	s.writePDF(w, r, m, T(ctx, "pdf.fileOverview")+"-"+month+".pdf")
}

func (s *Server) handleExportStatistics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	hh, ok := s.exportHousehold(w, r)
	if !ok {
		return
	}
	month := NormalizeMonth(r.URL.Query().Get("m"))
	vm, err := s.buildDashboardVM(r.Context(), hh.ID, month, periodYear, calc.Everyone, calc.GroupCategory, nil)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	m := newPDF()
	pdfHeader(ctx, m, T(ctx, "pdf.statistics"), hh.Name, vm.RangeLabel)

	pdfKV(m, T(ctx, "pdf.avgIncome"), FormatEUR(vm.Report.IncomeCents))
	pdfKV(m, T(ctx, "pdf.avgExpenses"), FormatEUR(vm.Report.ExpenseCents))
	pdfKV(m, T(ctx, "pdf.avgBalance"), FormatEUR(vm.Report.BalanceCents))

	pdfHeading(m, T(ctx, "pdf.monthCourse"))
	pdfRow4(m, T(ctx, "pdf.month"), T(ctx, "overview.income"), T(ctx, "overview.expenses"), T(ctx, "overview.balance"), true)
	for _, rep := range vm.Trend {
		pdfRow4(m, MonthLabel(ctx, rep.Month),
			FormatEUR(rep.IncomeCents),
			FormatEUR(rep.ExpenseCents),
			FormatEUR(rep.BalanceCents), false)
	}

	if len(vm.Settlement.Transfers) > 0 {
		pdfHeading(m, T(ctx, "dash.settlement"))
		for _, tr := range vm.Settlement.Transfers {
			pdfKV(m, Tf(ctx, "dash.owes", tr.From.Name, tr.To.Name), FormatEUR(tr.Cents))
		}
	}

	s.writePDF(w, r, m, T(ctx, "pdf.fileStatistics")+"-"+month+".pdf")
}

func (s *Server) handleExportExpenses(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	hh, ok := s.exportHousehold(w, r)
	if !ok {
		return
	}
	month := NormalizeMonth(r.URL.Query().Get("m"))
	vm, err := s.buildBookingsVM(r.Context(), hh.ID, month, r.URL.Query().Get("s"))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	members, err := s.store.ListMembers(ctx, hh.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	memberName := make(map[int64]string, len(members))
	for _, mem := range members {
		memberName[mem.ID] = mem.Name
	}

	m := newPDF()
	pdfHeader(ctx, m, T(ctx, "pdf.expenseList"), hh.Name, MonthLabel(ctx, month))

	var grand int64
	for _, g := range groupByCategory(vm.Bookings) {
		pdfHeading(m, g.Category.Name+"  ("+FormatEUR(g.TotalCents)+" "+T(ctx, "bookings.perMonth")+")")
		pdfRow4(m, T(ctx, "pdf.label"), T(ctx, "pdf.amount"), T(ctx, "pdf.rhythm"), T(ctx, "pdf.monthly"), true)
		for _, row := range g.Bookings {
			pdfRow4(m,
				row.Booking.Name+"  ["+splitNames(ctx, row, memberName)+"]",
				FormatEUR(row.AmountCents()),
				RhythmLabel(ctx, row.Booking),
				FormatEUR(row.MonthlyCents()), false)
		}
		if g.Category.Classification == store.DirExpense {
			grand += g.TotalCents
		}
	}

	m.AddRow(4)
	pdfKV(m, T(ctx, "pdf.total"), FormatEUR(grand))

	s.writePDF(w, r, m, T(ctx, "pdf.fileBookingList")+"-"+pdfSlug(hh.Name)+".pdf")
}

// matrixLabelCols is how much of the grid the row caption takes; the rest is
// one column per month plus total, mean and median.
const matrixLabelCols = 5

// pdfMatrixRow prints one line of the year matrix.
func pdfMatrixRow(m core.Maroto, label string, cells []string, style fontstyle.Type, color *props.Color) {
	cols := make([]core.Col, 0, len(cells)+1)
	cols = append(cols, text.NewCol(matrixLabelCols, label,
		props.Text{Size: 7, Style: style, Color: color}))
	for _, c := range cells {
		cols = append(cols, text.NewCol(1, c,
			props.Text{Size: 6, Align: align.Right, Style: style, Color: color}))
	}
	m.AddRow(5, cols...)
}

// matrixCells is a row's figures in the order the table shows them.
func pdfCells(row calc.MatrixRow) []string {
	out := make([]string, 0, len(row.Cents)+3)
	for _, c := range row.Cents {
		out = append(out, MatrixCell(c))
	}
	return append(out,
		FormatEURShort(row.TotalCents),
		FormatEURShort(row.MeanCents),
		FormatEURShort(row.MedianCents))
}

// matrixShares is the percentage line printed under a category.
func pdfShares(row calc.MatrixRow) []string {
	out := make([]string, 0, len(row.Share)+3)
	for _, p := range row.Share {
		out = append(out, FormatPercent(p))
	}
	return append(out, FormatPercent(row.ShareTotal), "", "")
}

func (s *Server) handleExportYear(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	hh, ok := s.exportHousehold(w, r)
	if !ok {
		return
	}
	month := NormalizeMonth(r.URL.Query().Get("m"))
	member := parseID(r.URL.Query().Get("view"))
	vm, err := s.buildDashboardVM(ctx, hh.ID, month, periodYear, member, calc.GroupCategory, nil)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	subtitle := vm.RangeLabel
	if name := vm.ViewName(); name != "" {
		subtitle += "  ·  " + name
	}
	m := newWidePDF(matrixLabelCols + len(vm.Matrix.Months) + 3)
	pdfHeader(ctx, m, T(ctx, "matrix.title"), hh.Name, subtitle)

	head := make([]string, 0, len(vm.Matrix.Months)+3)
	for _, mo := range vm.Matrix.Months {
		head = append(head, MonthShort(ctx, mo))
	}
	head = append(head, T(ctx, "matrix.total"), T(ctx, "matrix.mean"), T(ctx, "matrix.median"))
	pdfMatrixRow(m, T(ctx, "matrix.row"), head, fontstyle.Bold, nil)

	for _, band := range vm.Matrix.Bands {
		pdfHeading(m, MatrixBandLabel(ctx, band.Key))
		for _, row := range band.Rows {
			label := row.Label
			if row.LabelKey != "" {
				label = T(ctx, row.LabelKey)
			}
			pdfMatrixRow(m, label, pdfCells(row), fontstyle.Normal, nil)
			// Print cannot be unfolded, so every booking is listed outright.
			for _, child := range row.Children {
				pdfMatrixRow(m, "   "+child.Label, pdfCells(child), fontstyle.Normal, pdfGrey)
			}
			if band.Key != calc.BandIncome {
				pdfMatrixRow(m, "   "+MatrixShareLabel(ctx), pdfShares(row), fontstyle.Italic, pdfGrey)
			}
		}
		pdfMatrixRow(m, T(ctx, band.Total.LabelKey), pdfCells(band.Total), fontstyle.Bold, nil)
	}

	m.AddRow(4)
	for _, row := range []calc.MatrixRow{vm.Matrix.Expense, vm.Matrix.Surplus} {
		pdfMatrixRow(m, T(ctx, row.LabelKey), pdfCells(row), fontstyle.Bold, nil)
	}

	s.writePDF(w, r, m, T(ctx, "pdf.fileYear")+"-"+vm.Matrix.Months[0][:4]+".pdf")
}

// groupByCategory rebuilds the category grouping the printed list is laid out
// by; the page itself shows one flat list.
func groupByCategory(rows []BookingRow) []CategoryGroup {
	at := make(map[int64]int, len(rows))
	out := make([]CategoryGroup, 0, len(rows))
	for _, r := range rows {
		i, ok := at[r.Category.ID]
		if !ok {
			i = len(out)
			at[r.Category.ID] = i
			out = append(out, CategoryGroup{Category: r.Category})
		}
		out[i].Bookings = append(out[i].Bookings, r)
		out[i].TotalCents += r.MonthlyCents()
	}
	return out
}

func splitNames(ctx context.Context, row BookingRow, names map[int64]string) string {
	if len(row.Splits) == 0 {
		return T(ctx, "overview.unassigned")
	}
	parts := make([]string, 0, len(row.Splits))
	for _, sp := range row.Splits {
		if n, ok := names[sp.MemberID]; ok {
			parts = append(parts, n)
		}
	}
	return strings.Join(parts, ", ")
}
