package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/daknoblo/Haushaltsbuch/internal/bankimport"
)

type uploadOutcome struct {
	Status       string   `json:"status"`
	URL          string   `json:"url"`
	Digest       string   `json:"digest"`
	Transactions int      `json:"transactions"`
	Accounts     int      `json:"accounts"`
	Detected     []string `json:"detected,omitempty"`
	Added        int      `json:"added"`
	Existing     int      `json:"existing"`
	Preserved    int      `json:"preserved"`
	AnalysisIDs  []string `json:"analysis_ids,omitempty"`
}

func bankAnalysisIDs(ids []int64) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, strconv.FormatInt(id, 10))
	}
	return out
}

var (
	errUploadContext  = errors.New("upload context changed")
	errFilesNotStored = errors.New("uploaded files are not retained")
)

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
