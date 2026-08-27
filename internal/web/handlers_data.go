package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

// restorePath is the one route that accepts more than a form, so the body limit
// knows about it without importing the router.
const restorePath = "/settings/restore"

// maxSnapshotBytes bounds an uploaded backup. A household book of a few
// thousand bookings stays well under it, and the parser never holds more than
// this in memory.
const maxSnapshotBytes = 16 << 20

func (s *Server) handleBackupDownload(w http.ResponseWriter, r *http.Request) {
	snap, err := s.store.Export(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	body, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	name := T(r.Context(), "pdf.fileBackup") + "-" + time.Now().Format("2006-01-02") + ".json"
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	_, _ = w.Write(body)
}

func (s *Server) handleBackupRestore(w http.ResponseWriter, r *http.Request) {
	// A file arrives as multipart, which ParseForm does not read. The body is
	// already capped by limitBody, so the parser cannot be handed more.
	// #nosec G120 -- bounded by limitBody and by maxSnapshotBytes here
	if err := r.ParseMultipartForm(maxSnapshotBytes); err != nil {
		s.clientError(w, r, http.StatusBadRequest, "error.backupTooLarge")
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	file, _, err := r.FormFile("snapshot")
	if err != nil {
		s.clientError(w, r, http.StatusBadRequest, "error.backupMissing")
		return
	}
	defer func() { _ = file.Close() }()

	var snap store.Snapshot
	dec := json.NewDecoder(file)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&snap); err != nil {
		s.clientError(w, r, http.StatusBadRequest, "error.backupUnreadable")
		return
	}

	if err := s.store.Import(r.Context(), snap); err != nil {
		if errors.Is(err, store.ErrBadSnapshot) {
			s.clientError(w, r, http.StatusBadRequest, "error.backupUnreadable")
			return
		}
		s.serverError(w, r, err)
		return
	}
	hxRefresh(w)
}

func (s *Server) handleDataReset(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	if err := s.store.Reset(r.Context()); err != nil {
		s.serverError(w, r, err)
		return
	}
	hxRefresh(w)
}

func (s *Server) handleBookingsReset(w http.ResponseWriter, r *http.Request) {
	active, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return
	}
	if !s.parseForm(w, r) {
		return
	}
	if err := s.store.ResetBookings(r.Context(), active); err != nil {
		s.serverError(w, r, err)
		return
	}
	hxRefresh(w)
}
