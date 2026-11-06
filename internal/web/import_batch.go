package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"github.com/daknoblo/Haushaltsbuch/internal/calc"
	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

func selectedImportRows(ctx context.Context, snapshot store.HouseholdSnapshot, ids []int64) []ImportRow {
	wanted := map[int64]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	months := map[string]bool{}
	for _, p := range snapshot.Bank.Transactions {
		if wanted[p.ID] {
			months[p.BookedOn[:7]] = true
		}
	}
	var out []ImportRow
	for month := range months {
		for _, row := range importRows(ctx, snapshot.Bank, calc.FromSnapshot(snapshot), month) {
			if wanted[row.Payment.ID] {
				out = append(out, row)
			}
		}
	}
	return out
}

func transferPartner(ledger store.BankLedger, id int64) (store.BankTransaction, bool) {
	accounts := map[int64]int64{}
	payments := map[int64]store.BankTransaction{}
	for _, batch := range ledger.Imports {
		accounts[batch.ID] = batch.AccountID
	}
	for _, p := range ledger.Transactions {
		payments[p.ID] = p
	}
	candidates := func(p store.BankTransaction) []store.BankTransaction {
		var out []store.BankTransaction
		if p.Decision != "pending" || p.AmountCents <= 0 || accounts[p.ImportID] == 0 {
			return out
		}
		for _, other := range ledger.Transactions {
			if other.ID != p.ID && other.Decision == "pending" && accounts[other.ImportID] != 0 &&
				accounts[other.ImportID] != accounts[p.ImportID] && other.Direction != p.Direction &&
				other.AmountCents == p.AmountCents && nearbyBankDates(other.BookedOn, p.BookedOn) {
				out = append(out, other)
			}
		}
		return out
	}
	found := candidates(payments[id])
	if len(found) != 1 || len(candidates(found[0])) != 1 {
		return store.BankTransaction{}, false
	}
	return found[0], true
}

func transferPairToken(a, b store.BankTransaction) string {
	if a.ID > b.ID {
		a, b = b, a
	}
	text := ""
	for _, p := range []store.BankTransaction{a, b} {
		text += fmt.Sprintf("%d|%d|%q|%s|%d|%d|%d|%q|%q|%t;", p.ID, p.ImportID, p.Decision,
			store.BankSourceFingerprint(p), p.CategoryID, p.PlanID, p.TransferAccountID, p.CostNature, p.BudgetClass, p.Settle)
	}
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// MatchKey groups correction candidates without exposing references in DOM attributes.
func (row ImportRow) MatchKey() string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d|%q|%q", row.AccountID, row.Payment.Direction, historyKey(row.Payment))))
	return hex.EncodeToString(sum[:])
}

// PlanURL links to normal booking navigation rather than an isolated editor page.
func (row ImportRow) PlanURL() string {
	return "/bookings?m=" + row.Payment.BookedOn[:7] + "&q=" + url.QueryEscape(row.PlanName)
}

// PlanDifference compares the payment with the plan's unnormalized charge.
func (row ImportRow) PlanDifference() string {
	difference := row.Payment.AmountCents - row.PlanAmount
	if difference > 0 {
		return "+" + FormatEUR(difference)
	}
	return FormatEUR(difference)
}

// StageCount separates proposal readiness from persisted review decisions.
func (vm ImportVM) StageCount(stage string) int {
	count := 0
	for _, row := range vm.Rows {
		if row.Stage == stage {
			count++
		}
	}
	return count
}

// AnalysisCount excludes reliable local matches and unchanged cached AI results.
func (vm ImportVM) AnalysisCount() int {
	count := 0
	for _, row := range vm.Rows {
		if analysisCandidate(row, vm.Data) {
			count++
		}
	}
	return count
}

// CopyCount limits correction candidates to this visible month and account.
func (vm ImportVM) CopyCount(source ImportRow) int {
	count := 0
	for _, row := range vm.Rows {
		if row.Payment.Decision == "pending" && !row.Closed && row.MatchKey() == source.MatchKey() {
			count++
		}
	}
	return count
}

// ReviewStageLabel describes readiness without promising classification accuracy.
func ReviewStageLabel(ctx context.Context, stage string) string {
	switch stage {
	case "ready":
		return T(ctx, "review.ready")
	case "suggested":
		return T(ctx, "review.suggested")
	case "needs":
		return T(ctx, "review.needs")
	case "done":
		return T(ctx, "review.done")
	default:
		return T(ctx, "review.all")
	}
}

// ReviewStageBadge highlights readiness separately from the persisted decision.
func ReviewStageBadge(stage string) string {
	switch stage {
	case "ready":
		return "badge badge-ok"
	case "suggested":
		return "badge badge-warn"
	case "needs":
		return "badge badge-danger"
	default:
		return "badge badge-muted"
	}
}

func (s *Server) handleBankBatch(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	household, ok := s.importHousehold(w, r)
	if !ok {
		return
	}
	generation, err := strconv.ParseUint(r.FormValue("upload_generation"), 10, 64)
	if err != nil {
		s.importPageError(w, r, errUploadContext)
		return
	}
	ids, err := paymentIDs(r.Form["payment"])
	if err != nil {
		s.importPageError(w, r, err)
		return
	}
	month := r.FormValue("month")
	if month == "" || NormalizeMonth(month) != month {
		s.importPageError(w, r, store.ErrInvalid)
		return
	}
	err = s.importPreviews.current(generation, func() error {
		return s.store.ReviewBankBatch(r.Context(), household, func(snapshot store.HouseholdSnapshot) ([]store.BankTransaction, error) {
			rows := selectedImportRows(r.Context(), snapshot, ids)
			if len(rows) != len(ids) {
				return nil, store.ErrNotFound
			}
			if r.FormValue("operation") == "transfer" {
				if len(rows) != 1 {
					return nil, store.ErrInvalid
				}
				row := rows[0]
				partner, valid := transferPartner(snapshot.Bank, row.Payment.ID)
				if !valid || row.Closed || row.TransferPairID != partner.ID ||
					row.TransferPairToken == "" || row.TransferPairToken != r.FormValue("pair_token") || row.Payment.BookedOn[:7] != month {
					return nil, errReviewChanged
				}
				_, otherAccount, err := findBankPayment(snapshot.Bank, partner.ID)
				if err != nil {
					return nil, err
				}
				p := row.original
				p.Decision, p.TransferAccountID, p.PlanID = "transfer", otherAccount.ID, 0
				partner.Decision, partner.TransferAccountID, partner.PlanID = "transfer", row.AccountID, 0
				return []store.BankTransaction{p, partner}, nil
			}
			var payments []store.BankTransaction
			for _, row := range rows {
				token, err := row.ReviewToken()
				if err != nil {
					return nil, err
				}
				if row.Payment.Decision != "pending" || row.Closed || row.Payment.BookedOn[:7] != month ||
					token != r.FormValue("token_"+strconv.FormatInt(row.Payment.ID, 10)) {
					return nil, errReviewChanged
				}
				switch r.FormValue("operation") {
				case "accept":
					if !row.CanQuick {
						return nil, errReviewChanged
					}
					p := row.Payment
					p.Decision = "accepted"
					payments = append(payments, p)
				case "category":
					p := row.Payment
					category := parseID(r.FormValue("category"))
					if !slices.ContainsFunc(snapshot.Categories, func(c store.Category) bool { return c.ID == category && c.Classification == p.Direction }) {
						return nil, store.ErrInvalid
					}
					p.CategoryID = category
					if p.PlanID != 0 {
						p.Splits, p.TagIDs = nil, nil
					}
					if nature := r.FormValue("nature"); nature != "" {
						p.CostNature = store.CostNature(nature)
						if !p.CostNature.Valid() {
							return nil, store.ErrInvalid
						}
					}
					if class := r.FormValue("class"); class != "" {
						p.BudgetClass = store.BudgetClass(class)
						if !p.BudgetClass.Valid() {
							return nil, store.ErrInvalid
						}
					}
					payments = append(payments, p)
				default:
					return nil, store.ErrInvalid
				}
			}
			return payments, nil
		})
	})
	if errors.Is(err, errImportPreview) {
		err = errUploadContext
	}
	if err != nil {
		s.importPageError(w, r, err)
		return
	}
	count := len(ids)
	if r.FormValue("operation") == "transfer" {
		count = 2
	}
	s.reviewActionResult(w, r, count)
}

func (s *Server) reviewActionResult(w http.ResponseWriter, r *http.Request, count int) {
	if r.Header.Get("Accept") != "application/json" {
		redirectImportMonth(w, r, NormalizeMonth(r.FormValue("month")))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(struct {
		Updated int `json:"updated"`
	}{count}); err != nil {
		s.logger.Warn("review action response could not be written", "err", err)
	}
}
