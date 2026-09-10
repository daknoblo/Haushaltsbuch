package web

import (
	"context"
	"strconv"
	"strings"
)

// StatisticsExportURL keeps the PDF on the same scope and period as its tiles.
func (v DashboardVM) StatisticsExportURL(month string) string {
	return "/export/statistics.pdf?m=" + month + "&p=" + cleanPeriod(v.PeriodKey) +
		"&view=" + strconv.FormatInt(v.ViewMember, 10)
}

// HasRecordedIncome distinguishes an unavailable result from a true zero.
func (v DashboardVM) HasRecordedIncome() bool { return len(v.RecordedMonths) > 0 }

// CalculationBasis names every included month, so gaps remain transparent.
func (v DashboardVM) CalculationBasis(ctx context.Context) string {
	if !v.HasRecordedIncome() {
		return T(ctx, "dash.incomeDataMissing")
	}
	months := make([]string, 0, len(v.RecordedMonths))
	for _, month := range v.RecordedMonths {
		months = append(months, MonthShort(ctx, month))
	}
	return Tf(ctx, "dash.calculationBasis", len(months), len(v.Trend), strings.Join(months, ", "))
}

// ReportMoney renders unknown dashboard amounts as unavailable, not zero.
func (v DashboardVM) ReportMoney(cents int64) string {
	if !v.HasRecordedIncome() {
		return "—"
	}
	return FormatEUR(cents)
}

// IncomeRate avoids presenting a percentage with a zero denominator as 0%.
func IncomeRate(income int64, rate float64) string {
	if income <= 0 {
		return "—"
	}
	return FormatPercent(rate)
}
