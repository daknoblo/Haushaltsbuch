package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/johnfercher/maroto/v2"
	"github.com/johnfercher/maroto/v2/pkg/components/text"
	"github.com/johnfercher/maroto/v2/pkg/config"
	"github.com/johnfercher/maroto/v2/pkg/consts/align"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/core"
	"github.com/johnfercher/maroto/v2/pkg/props"

	"github.com/daknoblo/Haushaltsbuch/internal/calc"
	"github.com/daknoblo/Haushaltsbuch/internal/store"
	"github.com/daknoblo/Haushaltsbuch/internal/web"
)

var pdfGrey = &props.Color{Red: 110, Green: 116, Blue: 130}

func newPDF() core.Maroto {
	cfg := config.NewBuilder().
		WithPageNumber().
		Build()
	return maroto.New(cfg)
}

func pdfHeader(m core.Maroto, title, household, subtitle string) {
	m.AddRow(12, text.NewCol(12, title, props.Text{Size: 18, Style: fontstyle.Bold}))
	m.AddRow(6, text.NewCol(12, household+"  ·  "+subtitle, props.Text{Size: 10, Color: pdfGrey}))
	m.AddRow(6, text.NewCol(12, "Erstellt am "+time.Now().Format("02.01.2006 15:04"), props.Text{Size: 8, Color: pdfGrey}))
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
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	_, _ = w.Write(doc.GetBytes())
}

func (s *Server) exportOverviewPDF(w http.ResponseWriter, r *http.Request) {
	active, err := s.store.ActiveHouseholdID()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if active == 0 {
		http.Error(w, "Kein aktiver Haushalt", http.StatusBadRequest)
		return
	}
	hh, _ := s.store.GetHousehold(active)
	month := web.NormalizeMonth(r.URL.Query().Get("m"))
	rep, err := s.buildMonthReport(active, month)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	m := newPDF()
	pdfHeader(m, "Übersicht", hh.Name, web.MonthLabel(month))

	pdfKV(m, "Einnahmen", web.FormatEUR(rep.IncomeCents))
	pdfKV(m, "Ausgaben", web.FormatEUR(rep.ExpenseCents))
	pdfKV(m, "Saldo", web.FormatEUR(rep.BalanceCents))

	pdfHeading(m, "Personen")
	pdfRow4(m, "Person", "Einnahmen", "Ausgaben", "Saldo", true)
	for _, mb := range rep.Members {
		pdfRow4(m, mb.Member.Name,
			web.FormatEUR(mb.IncomeCents),
			web.FormatEUR(mb.ExpenseCents),
			web.FormatEUR(mb.BalanceCents), false)
	}

	if len(rep.Sections) > 0 {
		pdfHeading(m, "Nach Sektion")
		for _, sec := range rep.Sections {
			pdfKV(m, sec.Label, web.FormatEUR(sec.Cents))
		}
	}

	pdfHeading(m, "Bedarf / Wunsch / Sparen")
	pdfKV(m, "Bedarf", web.FormatEUR(rep.ByBudgetClass[store.ClassNeed]))
	pdfKV(m, "Wunsch", web.FormatEUR(rep.ByBudgetClass[store.ClassWant]))
	pdfKV(m, "Sparen", web.FormatEUR(rep.ByBudgetClass[store.ClassSaving]))

	s.writePDF(w, r, m, "uebersicht-"+month+".pdf")
}

func (s *Server) exportStatisticsPDF(w http.ResponseWriter, r *http.Request) {
	active, err := s.store.ActiveHouseholdID()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if active == 0 {
		http.Error(w, "Kein aktiver Haushalt", http.StatusBadRequest)
		return
	}
	hh, _ := s.store.GetHousehold(active)
	month := web.NormalizeMonth(r.URL.Query().Get("m"))
	vm, err := s.buildStatisticsVM(active, month)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	m := newPDF()
	pdfHeader(m, "Statistiken", hh.Name, "Zeitraum bis "+web.MonthLabel(month))

	pdfKV(m, "Ø Einnahmen / Monat", web.FormatEUR(vm.AvgIncome))
	pdfKV(m, "Ø Ausgaben / Monat", web.FormatEUR(vm.AvgExpense))
	pdfKV(m, "Ø Saldo / Monat", web.FormatEUR(vm.AvgIncome-vm.AvgExpense))

	pdfHeading(m, "Monatsverlauf")
	pdfRow4(m, "Monat", "Einnahmen", "Ausgaben", "Saldo", true)
	for _, sm := range vm.Months {
		pdfRow4(m, web.MonthLabel(sm.Month),
			web.FormatEUR(sm.IncomeCents),
			web.FormatEUR(sm.ExpenseCents),
			web.FormatEUR(sm.BalanceCents), false)
	}

	s.writePDF(w, r, m, "statistiken-"+month+".pdf")
}

func (s *Server) exportExpensesPDF(w http.ResponseWriter, r *http.Request) {
	active, err := s.store.ActiveHouseholdID()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if active == 0 {
		http.Error(w, "Kein aktiver Haushalt", http.StatusBadRequest)
		return
	}
	hh, _ := s.store.GetHousehold(active)
	vm, err := s.buildExpensesVM(active)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	memberName := make(map[int64]string, len(vm.Members))
	for _, mem := range vm.Members {
		memberName[mem.ID] = mem.Name
	}

	m := newPDF()
	pdfHeader(m, "Ausgabenliste", hh.Name, "Alle Ausgaben")

	var grand int64
	for _, g := range vm.Groups {
		if len(g.Expenses) == 0 {
			continue
		}
		pdfHeading(m, g.Title()+"  ("+web.FormatEUR(g.TotalCents)+" / Monat)")
		pdfRow4(m, "Bezeichnung", "Betrag", "Rhythmus", "Monatlich", true)
		for _, row := range g.Expenses {
			pdfRow4(m,
				row.Expense.Name+"  ["+splitNames(row, memberName)+"]",
				web.FormatEUR(row.Expense.AmountCents),
				web.FrequencyLabel(row.Expense.Frequency),
				web.FormatEUR(calc.MonthlyCents(row.Expense)), false)
		}
		grand += g.TotalCents
	}

	m.AddRow(4)
	pdfKV(m, "Gesamt (monatlich normalisiert)", web.FormatEUR(grand))

	s.writePDF(w, r, m, "ausgaben-"+hh.Name+".pdf")
}

func splitNames(row web.ExpenseRow, names map[int64]string) string {
	if len(row.Splits) == 0 {
		return "alle"
	}
	parts := make([]string, 0, len(row.Splits))
	for _, sp := range row.Splits {
		if n, ok := names[sp.MemberID]; ok {
			parts = append(parts, n)
		}
	}
	return strings.Join(parts, ", ")
}
