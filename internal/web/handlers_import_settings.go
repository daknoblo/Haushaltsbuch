package web

import (
	"net/http"
	"slices"
	"strings"

	"github.com/daknoblo/Haushaltsbuch/internal/bankimport"
)

// ImportAISettingsVM separates global inference configuration from import data.
type ImportAISettingsVM struct {
	Enabled     bool
	Deployment  string
	Deployments []string
	Error       string
}

// SelectedAvailable keeps a missing deployment visible instead of selecting another.
func (v ImportAISettingsVM) SelectedAvailable() bool {
	return slices.Contains(v.Deployments, v.Deployment)
}

func (s *Server) importAISettings(r *http.Request) (ImportAISettingsVM, error) {
	vm := ImportAISettingsVM{Enabled: s.bankAI != nil}
	var err error
	if vm.Deployment, err = s.store.AIDeployment(r.Context()); err != nil {
		return vm, err
	}
	if s.bankAI != nil {
		vm.Deployments, err = s.bankAI.Deployments(r.Context())
		if err != nil {
			s.logger.Warn("bank import deployment discovery failed")
			vm.Error = T(r.Context(), "import.aiError")
		} else if len(vm.Deployments) == 0 {
			vm.Error = T(r.Context(), "settings.aiNoDeployments")
		}
	}
	return vm, nil
}

func (s *Server) handleImportAISettings(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	deployment := strings.TrimSpace(r.FormValue("deployment"))
	if deployment != "" {
		if s.bankAI == nil {
			s.clientError(w, r, http.StatusServiceUnavailable, "import.disabled")
			return
		}
		names, err := s.bankAI.Deployments(r.Context())
		if err != nil {
			s.bankError(w, r, err)
			return
		}
		if !slices.Contains(names, deployment) {
			s.bankError(w, r, bankimport.ErrDeployment)
			return
		}
	}
	if err := s.store.SetAIDeployment(r.Context(), deployment); err != nil {
		s.bankError(w, r, err)
		return
	}
	hxChanged(w)
}

func (s *Server) handleBankAccounts(w http.ResponseWriter, r *http.Request) {
	active, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return
	}
	members, err := s.store.ListMembers(r.Context(), active)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	accounts, err := s.store.ListBankAccounts(r.Context(), active)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, bankAccountSettings(active, members, accounts))
}

func (s *Server) handleBankAccountRename(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	active, ok := s.importHousehold(w, r)
	if !ok {
		return
	}
	if err := s.store.RenameBankAccount(r.Context(), active, parseID(r.PathValue("id")), r.FormValue("name")); err != nil {
		s.bankError(w, r, err)
		return
	}
	hxChanged(w)
}

func (s *Server) handleBankAccountDefault(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	active, ok := s.importHousehold(w, r)
	if !ok {
		return
	}
	if err := s.store.SetDefaultBankAccount(r.Context(), active, parseID(r.PathValue("id"))); err != nil {
		s.bankError(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings#bank-accounts", http.StatusSeeOther)
}

// Keep the bank-account panel in sync without replacing an in-flight member input.
func memberSettingsChanged(w http.ResponseWriter) {
	w.Header().Set("HX-Trigger", "hb:changed, hb:members-changed")
}
