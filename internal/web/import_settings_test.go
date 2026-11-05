package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/daknoblo/Haushaltsbuch/internal/bankimport"
	"github.com/daknoblo/Haushaltsbuch/internal/i18n"
	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

type unavailableBankImporter struct{}

func (unavailableBankImporter) Environment() []bankimport.EnvironmentVariable {
	return (bankimport.Identity{}).Environment()
}

func (unavailableBankImporter) Deployments(context.Context) ([]string, error) {
	return nil, bankimport.ErrUpstream
}
func (unavailableBankImporter) Extract(context.Context, string, string) ([]store.BankTransaction, error) {
	return nil, bankimport.ErrUpstream
}

func TestImportModelSettingsAreGlobalAndValidated(t *testing.T) {
	srv, handler, hh := newTestServer(t)
	mock := &testBankImporter{}
	srv.SetBankImporter(mock)
	ctx := t.Context()
	if w := get(t, handler, "/imports"); w.Code != http.StatusOK ||
		!strings.Contains(w.Body.String(), `href="/settings#import-ai"`) || strings.Contains(w.Body.String(), `name="deployment"`) {
		t.Fatal("unconfigured import must point to settings")
	}
	if mock.discoveryCalls != 0 {
		t.Fatal("import opened a model-discovery connection")
	}
	for _, lang := range []i18n.Lang{i18n.German, i18n.English} {
		r := httptest.NewRequest(http.MethodGet, "/settings", nil)
		r.Header.Set("Accept-Language", string(lang))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `hx-post="/settings/import-ai"`) ||
			!strings.Contains(w.Body.String(), `id="bank-accounts"`) || !strings.Contains(w.Body.String(), "synthetic-router") ||
			!strings.Contains(w.Body.String(), i18n.T(lang, "settings.importAIHint")) {
			t.Fatalf("settings missing %s model/bank configuration", lang)
		}
	}
	if mock.discoveryCalls != 2 {
		t.Fatalf("discovery calls = %d", mock.discoveryCalls)
	}
	if w := post(t, handler, "/settings/import-ai", url.Values{"deployment": {"synthetic-router"}}); w.Code != http.StatusNoContent {
		t.Fatalf("save model: %d %s", w.Code, w.Body)
	}
	other, err := srv.store.CreateHouseholdSeeded(ctx, "Other")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetActiveHousehold(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	if model, _ := srv.store.AIDeployment(ctx); model != "synthetic-router" {
		t.Fatal("household switch changed model")
	}
	before := mock.discoveryCalls
	if w := get(t, handler, "/imports"); w.Code != http.StatusOK ||
		!strings.Contains(w.Body.String(), `data-bank-upload`) || strings.Contains(w.Body.String(), `name="deployment"`) {
		t.Fatal("saved model not usable in other household")
	}
	if mock.discoveryCalls != before {
		t.Fatal("import should use stored settings without discovery")
	}
	if w := post(t, handler, "/settings/import-ai", url.Values{"deployment": {"unknown-model"}}); w.Code != http.StatusBadRequest {
		t.Fatalf("unlisted model accepted: %d", w.Code)
	}
	if model, _ := srv.store.AIDeployment(ctx); model != "synthetic-router" {
		t.Fatal("invalid selection changed model")
	}
	if err := srv.store.SetAIDeployment(ctx, "removed-model"); err != nil {
		t.Fatal(err)
	}
	w := get(t, handler, "/settings")
	if !strings.Contains(w.Body.String(), `<option value="removed-model" selected disabled>removed-model</option>`) {
		t.Fatal("missing model was silently replaced in selector")
	}
	srv.SetBankImporter(unavailableBankImporter{})
	w = get(t, handler, "/settings")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `role="alert"`) ||
		!strings.Contains(w.Body.String(), "removed-model") {
		t.Fatal("discovery error must preserve selected model")
	}
	if w := post(t, handler, "/settings/import-ai", url.Values{"deployment": {"synthetic-router"}}); w.Code != http.StatusBadGateway {
		t.Fatalf("unavailable discovery: %d", w.Code)
	}
	if model, _ := srv.store.AIDeployment(ctx); model != "removed-model" {
		t.Fatal("failed discovery changed model")
	}
	srv.SetBankImporter(nil)
	if w := post(t, handler, "/settings/import-ai", url.Values{"deployment": {"synthetic-router"}}); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled AI accepted model: %d", w.Code)
	}
	if w := post(t, handler, "/settings/import-ai", url.Values{"deployment": {""}}); w.Code != http.StatusNoContent {
		t.Fatalf("clear model: %d", w.Code)
	}
	if err := srv.store.SetActiveHousehold(ctx, hh.ID); err != nil {
		t.Fatal(err)
	}
	if model, _ := srv.store.AIDeployment(ctx); model != "" {
		t.Fatal("clearing not global")
	}
}

func TestBankAccountFieldsShareOneRowLayout(t *testing.T) {
	srv, handler, household := newTestServer(t)
	members, err := srv.store.ListMembers(t.Context(), household.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.CreateBankAccount(t.Context(), household.ID, store.BankAccount{MemberID: members[0].ID, Name: "Additional synthetic account"}); err != nil {
		t.Fatal(err)
	}
	accounts, err := srv.store.ListBankAccounts(t.Context(), household.ID)
	if err != nil {
		t.Fatal(err)
	}
	body := get(t, handler, "/settings/bank-accounts").Body.String()
	rows := len(accounts) + len(members)
	if strings.Count(body, `class="bank-account-row`) != rows || strings.Count(body, `class="bank-account-actions"`) != rows {
		t.Fatal("default, additional and new accounts do not share the same field/action columns")
	}
	if strings.Count(body, `data-settings-save`) != len(accounts) || strings.Count(body, `hx-swap="none"`) != len(accounts) {
		t.Fatal("account autosave behavior changed")
	}
}

func TestMemberChangesRefreshBankAccountSettings(t *testing.T) {
	srv, handler, hh := newTestServer(t)
	ctx := t.Context()
	w := post(t, handler, "/members", url.Values{"name": {"New person"}})
	if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("HX-Trigger"), "hb:members-changed") {
		t.Fatalf("member create must refresh accounts: %d %s", w.Code, w.Header())
	}
	members, err := srv.store.ListMembers(ctx, hh.ID)
	if err != nil {
		t.Fatal(err)
	}
	member := members[len(members)-1]
	accounts, err := srv.store.ListBankAccounts(ctx, hh.ID)
	if err != nil {
		t.Fatal(err)
	}
	var defaultAccount store.BankAccount
	for _, a := range accounts {
		if a.MemberID == member.ID {
			defaultAccount = a
		}
	}
	if defaultAccount.ID == 0 || !defaultAccount.Default {
		t.Fatal("member lacks automatic default")
	}
	body := get(t, handler, "/settings/bank-accounts").Body.String()
	if !strings.Contains(body, "New person") || !strings.Contains(body, defaultAccount.Name) ||
		!strings.Contains(body, `hb:members-changed from:body`) {
		t.Fatal("accounts fragment missing new member")
	}
	memberURL := "/members/" + strconv.FormatInt(member.ID, 10)
	w = post(t, handler, memberURL, url.Values{"name": {"Renamed person"}, "color": {"#123456"}})
	if w.Code != http.StatusNoContent || !strings.Contains(w.Header().Get("HX-Trigger"), "hb:members-changed") {
		t.Fatal("rename refresh missing")
	}
	if !strings.Contains(get(t, handler, "/settings/bank-accounts").Body.String(), "Renamed person") {
		t.Fatal("owner name stale")
	}
	hhForm := url.Values{"household": {strconv.FormatInt(hh.ID, 10)}}
	accountURL := "/settings/bank-accounts/" + strconv.FormatInt(defaultAccount.ID, 10)
	w = post(t, handler, accountURL+"/delete", hhForm)
	if w.Code != http.StatusConflict {
		t.Fatalf("default account deleted: %d", w.Code)
	}
	w = post(t, handler, "/settings/bank-accounts", url.Values{
		"household": {strconv.FormatInt(hh.ID, 10)}, "member": {strconv.FormatInt(member.ID, 10)}, "name": {"Extra bank"},
	})
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/settings#bank-accounts" {
		t.Fatal("account creation must return to settings")
	}
	accounts, _ = srv.store.ListBankAccounts(ctx, hh.ID)
	extra := accounts[len(accounts)-1]
	w = post(t, handler, "/settings/bank-accounts/"+strconv.FormatInt(extra.ID, 10)+"/default", hhForm)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("default change: %d %s", w.Code, w.Body)
	}
	w = post(t, handler, accountURL+"/delete", hhForm)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("former default deletion: %d", w.Code)
	}
	w = post(t, handler, memberURL+"/delete", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("HX-Trigger"), "hb:members-changed") {
		t.Fatal("unused member/default deletion failed")
	}
	accounts, _ = srv.store.ListBankAccounts(ctx, hh.ID)
	for _, a := range accounts {
		if a.MemberID == member.ID {
			t.Fatal("deleted member's unused account survived")
		}
	}
}

func TestBankSettingsRejectCrossHouseholdAndCrossOrigin(t *testing.T) {
	srv, handler, hh := newTestServer(t)
	other, err := srv.store.CreateHouseholdSeeded(t.Context(), "Other")
	if err != nil {
		t.Fatal(err)
	}
	accounts, _ := srv.store.ListBankAccounts(t.Context(), other.ID)
	for _, suffix := range []string{"", "/default", "/delete"} {
		w := post(t, handler, "/settings/bank-accounts/"+strconv.FormatInt(accounts[0].ID, 10)+suffix,
			url.Values{"household": {strconv.FormatInt(hh.ID, 10)}, "name": {"Forged"}})
		if w.Code != http.StatusNotFound {
			t.Fatalf("cross-household %s: %d", suffix, w.Code)
		}
	}
	r := httptest.NewRequest(http.MethodPost, "/settings/import-ai", strings.NewReader("deployment=forged"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "https://foreign.invalid")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin model settings: %d", w.Code)
	}
	if err := srv.store.DeleteMember(t.Context(), hh.ID, accounts[0].MemberID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("foreign member: %v", err)
	}
}
