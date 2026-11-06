package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/daknoblo/Haushaltsbuch/internal/bankimport"
	"github.com/daknoblo/Haushaltsbuch/internal/calc"
	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

type bankClassifier interface {
	Classify(context.Context, string, bankimport.ClassificationRequest) ([]bankimport.ClassificationSuggestion, error)
}

func bankAnalysisKey(p store.BankTransaction, data calc.Data) string {
	text := fmt.Sprintf("%d|%d|%s|%d|%d|%q|%q|%t", p.ID, p.ImportID, store.BankSourceFingerprint(p), p.CategoryID, p.PlanID, p.CostNature, p.BudgetClass, p.Settle)
	categories := slices.Clone(data.Categories)
	slices.SortFunc(categories, func(a, b store.Category) int {
		return strings.Compare(strconv.FormatInt(a.ID, 10), strconv.FormatInt(b.ID, 10))
	})
	for _, c := range categories {
		text += fmt.Sprintf("|%d:%q:%q", c.ID, c.Name, c.Classification)
	}
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func applyBankAnalysis(row *ImportRow, data calc.Data) {
	p := &row.Payment
	a := p.Analysis
	if p.Decision != "pending" || row.original.CategoryID != 0 || p.PlanID != 0 ||
		row.Suggestion == "history" || row.Suggestion == "rule" || row.Suggestion == "conflict" ||
		a == nil || a.Key != bankAnalysisKey(row.original, data) {
		return
	}
	row.AIReason = a.Reason
	if a.CategoryID == 0 {
		return
	}
	if !slices.ContainsFunc(data.Categories, func(c store.Category) bool { return c.ID == a.CategoryID && c.Classification == p.Direction }) {
		return
	}
	p.CategoryID, row.Suggestion = a.CategoryID, "ai"
	if a.CostNature != "" {
		p.CostNature = a.CostNature
	}
	if a.BudgetClass != "" {
		p.BudgetClass = a.BudgetClass
	}
}

func analysisCandidate(row ImportRow, data calc.Data) bool {
	return row.Payment.Decision == "pending" && !row.Closed && !row.Duplicate && row.TransferCandidate == "" &&
		row.original.CategoryID == 0 && row.Payment.PlanID == 0 && row.Suggestion != "conflict" &&
		(row.Category == "" || row.Suggestion == "account") &&
		(row.original.Analysis == nil || row.original.Analysis.Key != bankAnalysisKey(row.original, data))
}

func paymentIDs(values []string) ([]int64, error) {
	if len(values) == 0 || len(values) > 1000 {
		return nil, store.ErrInvalid
	}
	ids := make([]int64, 0, len(values))
	seen := map[int64]bool{}
	for _, value := range values {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 || seen[id] {
			return nil, store.ErrInvalid
		}
		ids, seen[id] = append(ids, id), true
	}
	return ids, nil
}

func (s *Server) handleBankAnalysis(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	household, ok := s.importHousehold(w, r)
	if !ok {
		return
	}
	classifier, ok := s.bankAI.(bankClassifier)
	if !ok {
		s.clientError(w, r, http.StatusServiceUnavailable, "review.aiUnavailable")
		return
	}
	generation, err := strconv.ParseUint(r.FormValue("upload_generation"), 10, 64)
	if err != nil || generation != s.importPreviews.epoch() {
		s.importPageError(w, r, errUploadContext)
		return
	}
	select {
	case s.analysisGate <- struct{}{}:
		defer func() { <-s.analysisGate }()
	default:
		s.clientError(w, r, http.StatusTooManyRequests, "review.aiBusy")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 180*time.Second)
	defer cancel()
	model, err := s.store.AIDeployment(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if model == "" {
		s.clientError(w, r, http.StatusServiceUnavailable, "review.aiUnavailable")
		return
	}
	snapshot, err := s.store.ReadHousehold(ctx, household)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	ids := r.Form["payment"]
	if len(ids) == 0 {
		month := r.FormValue("month")
		if month != NormalizeMonth(month) || month == "" {
			s.bankError(w, r, store.ErrInvalid)
			return
		}
		for _, p := range snapshot.Bank.Transactions {
			if p.Decision == "pending" && p.BookedOn[:7] == month {
				ids = append(ids, strconv.FormatInt(p.ID, 10))
			}
		}
	}
	if len(ids) == 0 {
		s.reviewActionResult(w, r, 0)
		return
	}
	if len(ids) > 1000 {
		s.clientError(w, r, http.StatusBadRequest, "review.limit")
		return
	}
	parsedIDs, err := paymentIDs(ids)
	if err != nil {
		s.bankError(w, r, err)
		return
	}
	data := calc.FromSnapshot(snapshot)
	rows := map[int64]ImportRow{}
	for _, row := range selectedImportRows(ctx, snapshot, parsedIDs) {
		rows[row.Payment.ID] = row
	}
	var candidates []ImportRow
	for _, id := range parsedIDs {
		row, exists := rows[id]
		if !exists {
			s.bankError(w, r, store.ErrNotFound)
			return
		}
		if analysisCandidate(row, data) {
			candidates = append(candidates, row)
		}
	}
	for start := 0; start < len(candidates); start += 50 {
		part := candidates[start:min(start+50, len(candidates))]
		request := bankimport.ClassificationRequest{Language: LangCode(ctx)}
		for _, c := range data.Categories {
			request.Categories = append(request.Categories, bankimport.ClassificationCategory{ID: c.ID, Name: c.Name, Direction: c.Classification})
		}
		originals := map[int64]store.BankTransaction{}
		for _, row := range part {
			p := row.original
			originals[p.ID] = p
			request.Transactions = append(request.Transactions, bankimport.ClassificationPayment{ID: p.ID, Name: p.Name, Reference: p.Reference, Direction: p.Direction})
		}
		result, err := classifier.Classify(ctx, model, request)
		if err == nil {
			// Validate the interface boundary as well as the Azure wire response.
			var encoded []byte
			encoded, err = json.Marshal(struct {
				Suggestions []bankimport.ClassificationSuggestion `json:"suggestions"`
			}{result})
			if err == nil {
				result, err = bankimport.ParseClassification(string(encoded), request)
			}
		}
		if err != nil {
			s.logger.Warn("bank classification failed", "completed_batches", start/50)
			s.clientError(w, r, http.StatusBadGateway, "review.aiFailed")
			return
		}
		updates := make([]store.BankAnalysisUpdate, 0, len(result))
		for _, suggestion := range result {
			p := originals[suggestion.ID]
			analysis := store.BankAnalysis{Key: bankAnalysisKey(p, data), Reason: strings.TrimSpace(suggestion.Reason)}
			if suggestion.CategoryID != nil {
				analysis.CategoryID = *suggestion.CategoryID
			}
			if suggestion.CostNature != nil {
				analysis.CostNature = *suggestion.CostNature
			}
			if suggestion.BudgetClass != nil {
				analysis.BudgetClass = *suggestion.BudgetClass
			}
			updates = append(updates, store.BankAnalysisUpdate{Expected: p, Analysis: analysis})
		}
		err = s.importPreviews.current(generation, func() error { return s.store.SaveBankAnalyses(ctx, household, model, snapshot.Categories, updates) })
		if err != nil {
			if errors.Is(err, errImportPreview) {
				err = errUploadContext
			}
			if errors.Is(err, store.ErrInvalid) {
				s.clientError(w, r, http.StatusConflict, "review.analysisChanged")
			} else {
				s.importPageError(w, r, err)
			}
			return
		}
	}
	s.reviewActionResult(w, r, len(candidates))
}
