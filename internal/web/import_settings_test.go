package web

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/net/html"

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

func TestDefaultAccountDeleteRemainsVisibleAndDisabled(t *testing.T) {
	var body bytes.Buffer
	if err := bankAccountSettings(1, []store.Member{{ID: 1, Name: "Example"}},
		[]store.BankAccount{{ID: 1, MemberID: 1, Name: "Default", Default: true}, {ID: 2, MemberID: 1, Name: "Additional"}},
	).Render(t.Context(), &body); err != nil {
		t.Fatal(err)
	}
	document, err := html.Parse(&body)
	if err != nil {
		t.Fatal(err)
	}
	var buttons []map[string]string
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "button" {
			attributes := map[string]string{}
			for _, attribute := range node.Attr {
				attributes[attribute.Key] = attribute.Val
			}
			if attributes["aria-label"] == T(t.Context(), "import.deleteUnusedAccount") {
				buttons = append(buttons, attributes)
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(document)
	if len(buttons) != 2 {
		t.Fatalf("delete controls = %d, want one per account", len(buttons))
	}
	if _, disabled := buttons[0]["disabled"]; !disabled || buttons[0]["type"] != "button" || buttons[0]["title"] != T(t.Context(), "settings.defaultAccountProtected") {
		t.Fatal("default delete icon must be visible, natively disabled and explain the restriction")
	}
	if _, disabled := buttons[1]["disabled"]; disabled || buttons[1]["type"] != "submit" {
		t.Fatal("additional account deletion was disabled")
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
	if strings.Count(body, `class="settings-row`) != 2*rows || strings.Count(body, `class="settings-row-actions"`) != rows {
		t.Fatal("default, additional and new accounts do not share the same field/action columns")
	}
	if strings.Count(body, `data-settings-save`) != len(accounts) || strings.Count(body, `hx-swap="none"`) != len(accounts) {
		t.Fatal("account autosave behavior changed")
	}
}

func TestSettingsTilesShareTextAndActionColumns(t *testing.T) {
	srv, handler, household := newTestServer(t)
	if _, err := srv.store.CreateHouseholdSeeded(t.Context(), "Another synthetic household"); err != nil {
		t.Fatal(err)
	}
	households, err := srv.store.ListHouseholds(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	members, err := srv.store.ListMembers(t.Context(), household.ID)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := srv.store.ListBankAccounts(t.Context(), household.ID)
	if err != nil {
		t.Fatal(err)
	}
	attribute := func(node *html.Node, key string) string {
		for _, attr := range node.Attr {
			if attr.Key == key {
				return attr.Val
			}
		}
		return ""
	}
	for _, lang := range []i18n.Lang{i18n.German, i18n.English} {
		t.Run(string(lang), func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/settings", nil)
			request.Header.Set("Accept-Language", string(lang))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("settings: %d", response.Code)
			}
			document, err := html.Parse(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			rows, memberForms := 0, 0
			var visit func(*html.Node)
			visit = func(node *html.Node) {
				if strings.Contains(" "+attribute(node, "class")+" ", " settings-row ") {
					rows++
					var columns []*html.Node
					for child := node.FirstChild; child != nil; child = child.NextSibling {
						if child.Type == html.ElementNode && attribute(child, "type") != "hidden" {
							columns = append(columns, child)
						}
					}
					if len(columns) != 2 || attribute(columns[1], "class") != "settings-row-actions" {
						t.Fatal("every row needs a name column followed by the shared action column")
					}
					name := columns[0]
					if name.Data == "form" {
						name = name.FirstChild
						for name != nil && (name.Data != "input" || attribute(name, "type") == "hidden") {
							name = name.NextSibling
						}
					}
					if name == nil || name.Data != "input" || attribute(name, "name") != "name" {
						t.Fatal("text field must be the first visible control")
					}
					if node.Data == "form" && strings.HasPrefix(attribute(node, "hx-post"), "/members/") {
						memberForms++
						color := columns[1].FirstChild
						if color == nil || attribute(color, "type") != "color" ||
							attribute(node, "hx-swap") != "none" || attribute(node, "hx-trigger") != "change, keyup changed delay:600ms" {
							t.Fatal("color must follow the name inside the same autosave form")
						}
					}
				}
				if node.Data == "button" && strings.HasPrefix(attribute(node, "hx-post"), "/members/") &&
					attribute(node, "type") != "button" {
					t.Fatal("member action must not submit the enclosing autosave form")
				}
				for child := node.FirstChild; child != nil; child = child.NextSibling {
					visit(child)
				}
			}
			visit(document)
			if want := len(households) + 2*len(members) + len(accounts) + 2; rows != want || memberForms != len(members) {
				t.Fatalf("rows = %d (want %d), member forms = %d (want %d)", rows, want, memberForms, len(members))
			}
		})
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
