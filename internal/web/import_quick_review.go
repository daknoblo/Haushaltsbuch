package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/daknoblo/Haushaltsbuch/internal/calc"
	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

var errReviewChanged = errors.New("import review settings changed")

func pendingReview(r *http.Request, p store.BankTransaction) (store.BankTransaction, error) {
	p.Name, p.Reference = strings.TrimSpace(r.FormValue("name")), strings.TrimSpace(r.FormValue("reference"))
	p.BookedOn, p.Direction = r.FormValue("date"), store.Direction(r.FormValue("direction"))
	if strings.TrimSpace(r.FormValue("amount")) == "" {
		return p, store.ErrInvalid
	}
	var err error
	p.AmountCents, err = ParseCents(r.FormValue("amount"))
	if err != nil {
		return p, store.ErrInvalid
	}
	p.CategoryID, p.PlanID = parseID(r.FormValue("category")), parseID(r.FormValue("plan"))
	p.CostNature, p.BudgetClass = store.CostNature(r.FormValue("nature")), store.BudgetClass(r.FormValue("class"))
	p.Settle = r.FormValue("settle") == "yes"
	p.TransferAccountID = parseID(r.FormValue("transfer_account"))
	return p, nil
}

func quickBankPayment(r *http.Request, snapshot store.HouseholdSnapshot) (store.BankTransaction, error) {
	id := parseID(r.PathValue("id"))
	original, _, err := findBankPayment(snapshot.Bank, id)
	if err != nil {
		return store.BankTransaction{}, err
	}
	for _, row := range importRows(r.Context(), snapshot.Bank, calc.FromSnapshot(snapshot), original.BookedOn[:7]) {
		if row.Payment.ID != id {
			continue
		}
		token, err := row.ReviewToken()
		if err != nil {
			return store.BankTransaction{}, err
		}
		if !row.CanQuick || r.FormValue("review_token") != token || r.FormValue("allocation") != row.Allocation || r.FormValue("allow_duplicate") != "" {
			return store.BankTransaction{}, errReviewChanged
		}
		edited, err := pendingReview(r, row.original)
		if err != nil {
			return store.BankTransaction{}, err
		}
		edited.Splits, edited.TagIDs = row.Payment.Splits, row.Payment.TagIDs
		if !reflect.DeepEqual(edited, row.Payment) {
			return store.BankTransaction{}, errReviewChanged
		}
		edited.Decision = "accepted"
		return edited, nil
	}
	return store.BankTransaction{}, store.ErrNotFound
}

func (s *Server) handleQuickBankReview(w http.ResponseWriter, r *http.Request, household int64) {
	generation, err := strconv.ParseUint(r.FormValue("upload_generation"), 10, 64)
	if err != nil {
		s.importPageError(w, r, errUploadContext)
		return
	}
	var month, markup string
	id := parseID(r.PathValue("id"))
	err = s.importPreviews.current(generation, func() error {
		if err := s.store.ReviewBankSuggestion(r.Context(), household, func(snapshot store.HouseholdSnapshot) (store.BankTransaction, error) {
			payment, err := quickBankPayment(r, snapshot)
			if err == nil {
				month = payment.BookedOn[:7]
			}
			return payment, err
		}); err != nil {
			return err
		}
		if r.Header.Get("Accept") != "application/json" {
			return nil
		}
		snapshot, err := s.store.ReadHousehold(r.Context(), household)
		if err != nil {
			return err
		}
		vm := ImportVM{Ledger: snapshot.Bank, Data: calc.FromSnapshot(snapshot), UploadGeneration: generation}
		vm.Rows = importRows(r.Context(), vm.Ledger, vm.Data, month)
		for _, row := range vm.Rows {
			if row.Payment.ID != id {
				continue
			}
			var body bytes.Buffer
			nav := Nav{Month: month, ActiveHousehold: store.Household{ID: household}}
			if err := importPayment(nav, vm, row).Render(r.Context(), &body); err != nil {
				return err
			}
			markup = body.String()
			return nil
		}
		return store.ErrNotFound
	})
	if err != nil {
		if errors.Is(err, errImportPreview) {
			err = errUploadContext
		}
		s.importPageError(w, r, err)
		return
	}
	if r.Header.Get("Accept") != "application/json" {
		redirectImportMonth(w, r, month)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(struct {
		ID   string `json:"id"`
		HTML string `json:"html"`
	}{strconv.FormatInt(id, 10), markup}); err != nil {
		s.logger.Warn("review response could not be written", "err", err)
	}
}
