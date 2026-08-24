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

func pdfHeader(ctx context.Context, m core.Maroto, title, household, subtitle string) {
	m.AddRow(12, text.NewCol(12, title, props.Text{Size: 18, Style: fontstyle.Bold}))
	m.AddRow(6, text.NewCol(12, household+"  ·  "+subtitle, props.Text{Size: 10, Color: pdfGrey}))
	m.AddRow(6, text.NewCol(12, Tf(ctx, "pdf.createdAt", time.Now().Format("02.01.2006 15:04")), props.Text{Size: 8, Color: pdfGrey}))
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

func (s *Server) exportOverviewPDF(w http.ResponseWriter, r *http.Request) {
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
	pdfHeader(ctx, m, T(ctx, "pdf.overview"), hh.Name, MonthLabel(month))

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

	if len(rep.Sections) > 0 {
		pdfHeading(m, T(ctx, "overview.bySection"))
		for _, sec := range rep.Sections {
			pdfKV(m, sec.Label, FormatEUR(sec.Cents))
		}
	}

	pdfHeading(m, T(ctx, "overview.budgetClasses"))
	pdfKV(m, T(ctx, "class.need"), FormatEUR(rep.ByBudgetClass[store.ClassNeed]))
	pdfKV(m, T(ctx, "class.want"), FormatEUR(rep.ByBudgetClass[store.ClassWant]))
	pdfKV(m, T(ctx, "class.saving"), FormatEUR(rep.ByBudgetClass[store.ClassSaving]))

	s.writePDF(w, r, m, "uebersicht-"+month+".pdf")
}

func (s *Server) exportStatisticsPDF(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	hh, ok := s.exportHousehold(w, r)
	if !ok {
		return
	}
	month := NormalizeMonth(r.URL.Query().Get("m"))
	vm, err := s.buildStatisticsVM(r.Context(), hh.ID, month)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	m := newPDF()
	pdfHeader(ctx, m, T(ctx, "pdf.statistics"), hh.Name, Tf(ctx, "pdf.periodUntil", MonthLabel(month)))

	pdfKV(m, T(ctx, "pdf.avgIncome"), FormatEUR(vm.AvgIncome))
	pdfKV(m, T(ctx, "pdf.avgExpenses"), FormatEUR(vm.AvgExpense))
	pdfKV(m, T(ctx, "pdf.avgBalance"), FormatEUR(vm.AvgIncome-vm.AvgExpense))

	pdfHeading(m, T(ctx, "pdf.monthCourse"))
	pdfRow4(m, T(ctx, "pdf.month"), T(ctx, "overview.income"), T(ctx, "overview.expenses"), T(ctx, "overview.balance"), true)
	for _, sm := range vm.Months {
		pdfRow4(m, MonthLabel(sm.Month),
			FormatEUR(sm.IncomeCents),
			FormatEUR(sm.ExpenseCents),
			FormatEUR(sm.BalanceCents), false)
	}

	s.writePDF(w, r, m, "statistiken-"+month+".pdf")
}

func (s *Server) exportExpensesPDF(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	hh, ok := s.exportHousehold(w, r)
	if !ok {
		return
	}
	vm, err := s.buildExpensesVM(r.Context(), hh.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	memberName := make(map[int64]string, len(vm.Members))
	for _, mem := range vm.Members {
		memberName[mem.ID] = mem.Name
	}

	m := newPDF()
	pdfHeader(ctx, m, T(ctx, "pdf.expenseList"), hh.Name, T(ctx, "pdf.allExpenses"))

	var grand int64
	for _, g := range vm.Groups {
		if len(g.Expenses) == 0 {
			continue
		}
		pdfHeading(m, g.Title()+"  ("+FormatEUR(g.TotalCents)+" / Monat)")
		pdfRow4(m, T(ctx, "pdf.label"), T(ctx, "pdf.amount"), T(ctx, "pdf.rhythm"), T(ctx, "pdf.monthly"), true)
		for _, row := range g.Expenses {
			pdfRow4(m,
				row.Expense.Name+"  ["+splitNames(ctx, row, memberName)+"]",
				FormatEUR(row.Expense.AmountCents),
				FrequencyLabel(ctx, row.Expense.Frequency),
				FormatEUR(calc.MonthlyCents(row.Expense)), false)
		}
		grand += g.TotalCents
	}

	m.AddRow(4)
	pdfKV(m, T(ctx, "pdf.total"), FormatEUR(grand))

	s.writePDF(w, r, m, "ausgaben-"+pdfSlug(hh.Name)+".pdf")
}

func splitNames(ctx context.Context, row ExpenseRow, names map[int64]string) string {
	if len(row.Splits) == 0 {
		return T(ctx, "pdf.everyone")
	}
	parts := make([]string, 0, len(row.Splits))
	for _, sp := range row.Splits {
		if n, ok := names[sp.MemberID]; ok {
			parts = append(parts, n)
		}
	}
	return strings.Join(parts, ", ")
}
