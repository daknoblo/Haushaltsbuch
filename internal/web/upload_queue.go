package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"

	"github.com/daknoblo/Haushaltsbuch/internal/bankimport"
	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

type uploadOutcome struct {
	Status       string   `json:"status"`
	URL          string   `json:"url"`
	Digest       string   `json:"digest"`
	Transactions int      `json:"transactions"`
	Accounts     int      `json:"accounts"`
	Detected     []string `json:"detected,omitempty"`
}

var errUploadContext = errors.New("upload context changed")

func (s *Server) uploadResult(w http.ResponseWriter, r *http.Request, result uploadOutcome) {
	if r.Header.Get("Accept") != "application/json" {
		http.Redirect(w, r, result.URL, http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	status := http.StatusCreated
	if result.Status == "mapping" {
		status = http.StatusAccepted
	}
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(result); err != nil {
		s.logger.Warn("upload response could not be written", "err", err)
	}
}

// UploadedBatch retains the per-account management actions of a source file.
type UploadedBatch struct {
	Import  store.BankImport
	Account string
}

// UploadedFile summarizes persisted imports of identical file contents.
type UploadedFile struct {
	Filename     string
	Digest       string
	CreatedAt    string
	Batches      []UploadedBatch
	Transactions int
	Pending      int
	ReviewURL    string
	lastID       int64
}

func uploadedFiles(ctx context.Context, ledger store.BankLedger, members []store.Member) []UploadedFile {
	accounts := make(map[int64]string, len(ledger.Accounts))
	for _, account := range ledger.Accounts {
		accounts[account.ID] = BankAccountLabel(ctx, account, members)
	}
	byDigest := make(map[string]int)
	byImport := make(map[int64]int, len(ledger.Imports))
	var out []UploadedFile
	for _, batch := range ledger.Imports {
		index, exists := byDigest[batch.Digest]
		if !exists {
			index = len(out)
			byDigest[batch.Digest] = index
			out = append(out, UploadedFile{Digest: batch.Digest})
		}
		file := &out[index]
		if batch.ID > file.lastID {
			file.Filename, file.CreatedAt, file.lastID = batch.Filename, batch.CreatedAt, batch.ID
		}
		file.Batches = append(file.Batches, UploadedBatch{Import: batch, Account: accounts[batch.AccountID]})
		byImport[batch.ID] = index
	}
	for _, payment := range ledger.Transactions {
		index, ok := byImport[payment.ImportID]
		if !ok {
			continue
		}
		file := &out[index]
		file.Transactions++
		if payment.Decision == "pending" {
			file.Pending++
			if file.Pending == 1 {
				file.ReviewURL = importMonthURL(payment.BookedOn[:7])
			}
		}
		if file.ReviewURL == "" {
			file.ReviewURL = importMonthURL(payment.BookedOn[:7])
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].lastID > out[j].lastID })
	return out
}

func (s *Server) handleImportStatus(w http.ResponseWriter, r *http.Request) {
	household, ok := s.importHousehold(w, r)
	if !ok {
		return
	}
	nav, err := s.buildNav(r, "imports", "/imports", true)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if nav.ActiveHousehold.ID != household {
		s.clientError(w, r, http.StatusConflict, "import.householdChanged")
		return
	}
	vm, err := s.buildImportVM(r.Context(), household, nav.Month)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	switch r.URL.Query().Get("part") {
	case "history":
		s.render(w, r, ImportUploadHistory(nav, vm))
	case "review":
		s.render(w, r, ImportReview(nav, vm))
	default:
		s.clientError(w, r, http.StatusBadRequest, "error.invalidInput")
	}
}

// UploadByteLimit keeps client-side checks aligned with the actual server limit.
func UploadByteLimit() int { return bankimport.MaxFileBytes }
