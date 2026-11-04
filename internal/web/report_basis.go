package web

import (
	"context"
	"net/http"
	"time"

	"github.com/daknoblo/Haushaltsbuch/internal/calc"
)

type reportBasisKey struct{}

func reportBasis(ctx context.Context) calc.Basis {
	if basis, ok := ctx.Value(reportBasisKey{}).(calc.Basis); ok {
		return basis
	}
	return calc.BasisHybrid
}

func withReportBasis(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		basis := calc.Basis(r.URL.Query().Get("basis"))
		if basis == "" {
			basis = calc.BasisHybrid
		}
		if !basis.Valid() {
			http.Error(w, T(r.Context(), "error.invalidInput"), http.StatusBadRequest)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), reportBasisKey{}, basis)))
	})
}

func (s *Server) loadReportData(ctx context.Context, householdID int64) (calc.Data, error) {
	data, err := s.loadHouseholdData(ctx, householdID)
	if err != nil {
		return calc.Data{}, err
	}
	ledger, err := s.store.BankLedger(ctx, householdID)
	if err != nil {
		return calc.Data{}, err
	}
	return calc.WithBankLedger(data, ledger, reportBasis(ctx), time.Now().Format("2006-01")), nil
}

// SourceLabel distinguishes confirmed actuals from partial data and estimates.
func SourceLabel(ctx context.Context, source string) string {
	switch source {
	case "actual":
		return T(ctx, "basis.actual")
	case "actual_partial":
		return T(ctx, "basis.actual_partial")
	case "plan_estimate":
		return T(ctx, "basis.plan_estimate")
	case "hybrid":
		return T(ctx, "basis.hybrid")
	default:
		return T(ctx, "basis.plan")
	}
}

// BasisLabel names the view without implying that its months are complete.
func BasisLabel(ctx context.Context, basis string) string {
	if basis == "actual" {
		return T(ctx, "basis.actualView")
	}
	return SourceLabel(ctx, basis)
}

// ShortSource fits the matrix while its title carries the full explanation.
func ShortSource(ctx context.Context, source string) string {
	switch source {
	case "actual":
		return T(ctx, "basis.shortActual")
	case "actual_partial":
		return T(ctx, "basis.shortPartial")
	case "plan_estimate":
		return T(ctx, "basis.shortEstimate")
	default:
		return T(ctx, "basis.shortPlan")
	}
}

// ImportDecisionLabel names the persisted review decision.
func ImportDecisionLabel(ctx context.Context, decision string) string {
	switch decision {
	case "accepted":
		return T(ctx, "import.accepted")
	case "ignored":
		return T(ctx, "import.ignored")
	case "transfer":
		return T(ctx, "import.transfer")
	default:
		return T(ctx, "import.pending")
	}
}

// BasisURL preserves the selected period and member while changing basis.
func (n Nav) BasisURL(basis string) string {
	return n.Path + "?" + n.ReportQuery + "&basis=" + basis
}

// SourceSummary states the basis of every month included in a chart or PDF.
func (v DashboardVM) SourceSummary(ctx context.Context) string {
	out := T(ctx, "basis.explanation")
	for _, report := range v.Trend {
		out += " · " + MonthShort(ctx, report.Month) + ": " + SourceLabel(ctx, report.Source)
	}
	return out
}
