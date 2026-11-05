package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/daknoblo/Haushaltsbuch/internal/bankimport"
	"github.com/daknoblo/Haushaltsbuch/internal/calc"
	"github.com/daknoblo/Haushaltsbuch/internal/i18n"
	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

// BankImporter is the optional, read-only AI suggestion service.
type BankImporter interface {
	Environment() []bankimport.EnvironmentVariable
	Deployments(context.Context) ([]string, error)
	Extract(context.Context, string, string) ([]store.BankTransaction, error)
}

// SetBankImporter wires the optional service before serving requests.
func (s *Server) SetBankImporter(importer BankImporter) { s.bankAI = importer }

// ImportVM combines persisted review state with global AI readiness.
type ImportVM struct {
	Ledger           store.BankLedger
	Data             calc.Data
	Rows             []ImportRow
	AIEnabled        bool
	AIConfigured     bool
	Closed           bool
	PendingMappings  []ImportMappingLink
	Uploads          []UploadedFile
	UploadGeneration uint64
}

// ImportRow adds non-binding duplicate, transfer and plan hints to a payment.
type ImportRow struct {
	Payment           store.BankTransaction
	AccountID         int64
	Account           string
	Source            string
	Duplicate         bool
	TransferCandidate string
	PlanAmount        int64
	PlanName          string
}

// DecisionCount summarizes the visible month's review state without mixing amounts.
func (vm ImportVM) DecisionCount(decision string) int {
	count := 0
	for _, row := range vm.Rows {
		if row.Payment.Decision == decision {
			count++
		}
	}
	return count
}

// SignedAmount distinguishes statement inflow and outflow at a glance.
func (row ImportRow) SignedAmount() string {
	if row.Payment.Direction == store.DirIncome {
		return "+" + FormatEUR(row.Payment.AmountCents)
	}
	return "-" + FormatEUR(row.Payment.AmountCents)
}

// URL addresses the household-scoped review endpoint.
func (row ImportRow) URL() string {
	return "/imports/transactions/" + strconv.FormatInt(row.Payment.ID, 10)
}

func (s *Server) handleImports(w http.ResponseWriter, r *http.Request) {
	nav, err := s.buildNav(r, "imports", "/imports", true)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	vm, err := s.buildImportVM(r.Context(), nav.ActiveHousehold.ID, nav.Month)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, ImportsPage(nav, vm))
}

func (s *Server) buildImportVM(ctx context.Context, household int64, month string) (ImportVM, error) {
	vm := ImportVM{AIEnabled: s.bankAI != nil, UploadGeneration: s.importPreviews.epoch()}
	deployment, err := s.store.AIDeployment(ctx)
	if err != nil {
		return vm, err
	}
	vm.AIConfigured = deployment != ""
	if household != 0 {
		vm.Ledger, err = s.store.BankLedger(ctx, household)
		if err != nil {
			return vm, err
		}
		vm.Data, err = s.loadHouseholdData(ctx, household)
		if err != nil {
			return vm, err
		}
		vm.Closed = slices.Contains(vm.Ledger.ClosedMonths, month)
		vm.Rows = importRows(ctx, vm.Ledger, vm.Data, month)
		vm.PendingMappings = s.importPreviews.list(household)
		vm.Uploads = uploadedFiles(ctx, vm.Ledger, vm.Data.Members)
	}
	return vm, nil
}

func importRows(ctx context.Context, l store.BankLedger, data calc.Data, month string) []ImportRow {
	accounts := make(map[int64]string)
	batches := make(map[int64]store.BankImport)
	plans := make(map[int64]store.Booking)
	for _, a := range l.Accounts {
		accounts[a.ID] = BankAccountLabel(ctx, a, data.Members)
	}
	for _, b := range l.Imports {
		batches[b.ID] = b
	}
	for _, b := range data.Bookings {
		plans[b.ID] = b
	}
	var out []ImportRow
	for _, p := range l.Transactions {
		if p.BookedOn[:7] != month {
			continue
		}
		batch := batches[p.ImportID]
		row := ImportRow{Payment: p, AccountID: batch.AccountID, Account: accounts[batch.AccountID], Source: batch.Filename}
		if plan, ok := plans[p.PlanID]; ok {
			row.PlanName = plan.Name
			row.PlanAmount = calc.AmountFor(plan, data.Overrides[plan.ID], month)
		}
		for _, other := range l.Transactions {
			if other.ID == p.ID || other.Decision == "ignored" {
				continue
			}
			otherAccount := batches[other.ImportID].AccountID
			if otherAccount == batch.AccountID && other.Fingerprint == p.Fingerprint {
				row.Duplicate = true
			}
			if otherAccount != batch.AccountID && p.AmountCents == other.AmountCents &&
				p.Direction != other.Direction && nearbyBankDates(p.BookedOn, other.BookedOn) {
				row.TransferCandidate = accounts[otherAccount]
			}
		}
		out = append(out, row)
	}
	slices.Reverse(out)
	return out
}

func nearbyBankDates(a, b string) bool {
	first, err := time.Parse("2006-01-02", a)
	if err != nil {
		return false
	}
	second, err := time.Parse("2006-01-02", b)
	return err == nil && first.Sub(second) <= 72*time.Hour && second.Sub(first) <= 72*time.Hour
}

// Each form carries its household to reject stale tabs after a household switch.
func (s *Server) importHousehold(w http.ResponseWriter, r *http.Request) (int64, bool) {
	hh, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return 0, false
	}
	if parseID(r.FormValue("household")) != hh {
		s.clientError(w, r, http.StatusConflict, "import.householdChanged")
		return 0, false
	}
	return hh, true
}

func (s *Server) handleBankAccount(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	hh, ok := s.importHousehold(w, r)
	if !ok {
		return
	}
	err := s.store.CreateBankAccount(r.Context(), hh, store.BankAccount{
		Name: strings.TrimSpace(r.FormValue("name")), MemberID: parseID(r.FormValue("member")),
	})
	if err != nil {
		s.bankError(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings#bank-accounts", http.StatusSeeOther)
}

func (s *Server) handleBankAccountDelete(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	hh, ok := s.importHousehold(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteBankAccount(r.Context(), hh, parseID(r.PathValue("id"))); err != nil {
		s.bankError(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings#bank-accounts", http.StatusSeeOther)
}

func (s *Server) handleBankImportDiscard(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	hh, ok := s.importHousehold(w, r)
	if !ok {
		return
	}
	if err := s.store.DiscardBankImport(r.Context(), hh, parseID(r.PathValue("id"))); err != nil {
		s.bankError(w, r, err)
		return
	}
	http.Redirect(w, r, "/imports", http.StatusSeeOther)
}

func (s *Server) handleBankUpload(w http.ResponseWriter, r *http.Request) {
	generation := s.importPreviews.epoch()
	select {
	case s.importGate <- struct{}{}:
		defer func() { <-s.importGate }()
	default:
		s.clientError(w, r, http.StatusTooManyRequests, "import.busy")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 180*time.Second)
	defer cancel()
	r.Body = http.MaxBytesReader(w, r.Body, 11<<20)
	if err := r.ParseMultipartForm(11 << 20); err != nil { // #nosec G120 -- body capped at 11 MiB above; memory parsing supports the read-only container.
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
		s.clientError(w, r, http.StatusBadRequest, "import.fileError")
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	if len(r.MultipartForm.File["statement"]) != 1 || len(r.MultipartForm.File) != 1 {
		s.clientError(w, r, http.StatusBadRequest, "import.singleRequestFile")
		return
	}
	hh, ok := s.importHousehold(w, r)
	if !ok {
		return
	}
	if raw := r.FormValue("upload_generation"); raw != "" {
		expected, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || expected != generation {
			s.bankError(w, r, errUploadContext)
			return
		}
	}
	accountID := parseID(r.FormValue("account"))
	ledger, err := s.store.BankLedger(ctx, hh)
	if err != nil {
		s.bankError(w, r, err)
		return
	}
	if !slices.ContainsFunc(ledger.Accounts, func(a store.BankAccount) bool { return a.ID == accountID }) {
		s.bankError(w, r, store.ErrNotFound)
		return
	}
	file, header, err := r.FormFile("statement")
	if err != nil {
		s.bankError(w, r, bankimport.ErrFile)
		return
	}
	defer func() { _ = file.Close() }()
	filename := filepath.Base(header.Filename)
	if utf8.RuneCountInString(filename) > 255 {
		s.bankError(w, r, bankimport.ErrFile)
		return
	}
	content, err := io.ReadAll(io.LimitReader(file, bankimport.MaxFileBytes+1))
	if err != nil || len(content) > bankimport.MaxFileBytes {
		s.bankError(w, r, bankimport.ErrFile)
		return
	}
	hash := sha256.Sum256(content)
	digest := hex.EncodeToString(hash[:])
	if strings.EqualFold(filepath.Ext(filename), ".pdf") {
		exists, err := s.store.HasBankImport(ctx, hh, accountID, digest)
		if err != nil {
			s.bankError(w, r, err)
			return
		}
		if exists {
			s.bankError(w, r, store.ErrDuplicateImport)
			return
		}
	}
	sources, err := s.extractStatement(ctx, r, filename, content)
	if err != nil {
		s.bankError(w, r, err)
		return
	}
	if len(sources) == 0 {
		s.bankError(w, r, bankimport.ErrResult)
		return
	}
	for _, source := range sources {
		if len(source.Payments) == 0 {
			s.bankError(w, r, bankimport.ErrResult)
			return
		}
	}
	data, err := s.loadHouseholdData(ctx, hh)
	if err != nil {
		s.bankError(w, r, err)
		return
	}
	for _, source := range sources {
		for i := range source.Payments {
			suggestPlan(&source.Payments[i], data)
		}
	}
	if len(sources) > 1 {
		token, err := s.importPreviews.put(generation, pendingImport{household: hh, digest: digest, filename: filename, sources: sources})
		if err != nil {
			if errors.Is(err, errImportPreview) {
				err = errUploadContext
			}
			s.bankError(w, r, err)
			return
		}
		count := 0
		for _, source := range sources {
			count += len(source.Payments)
		}
		s.uploadResult(w, r, uploadOutcome{Status: "mapping", URL: importMappingURL(token), Digest: digest, Transactions: count, Accounts: len(sources)})
		return
	}
	payments := sources[0].Payments
	err = s.importPreviews.current(generation, func() error {
		return s.store.CreateBankImport(ctx, hh, store.BankImport{
			AccountID: accountID, Digest: digest, Filename: filename,
		}, payments)
	})
	if err != nil {
		if errors.Is(err, errImportPreview) {
			err = errUploadContext
		}
		s.bankError(w, r, err)
		return
	}
	s.uploadResult(w, r, uploadOutcome{Status: "imported", URL: importMonthURL(payments[0].BookedOn[:7]), Digest: digest, Transactions: len(payments), Accounts: 1})
}

func suggestPlan(p *store.BankTransaction, data calc.Data) {
	var matches []store.Booking
	text := strings.ToLower(p.Name + " " + p.Reference)
	for _, b := range data.Bookings {
		name := strings.TrimSpace(strings.ToLower(b.Name))
		if name != "" && b.Direction == p.Direction && calc.ActiveIn(b, p.BookedOn[:7]) && strings.Contains(text, name) {
			matches = append(matches, b)
		}
	}
	if len(matches) == 1 {
		b := matches[0]
		p.PlanID, p.CategoryID, p.CostNature, p.BudgetClass, p.Settle = b.ID, b.CategoryID, b.CostNature, b.BudgetClass, b.Settle
	}
}

func findBankPayment(l store.BankLedger, id int64) (store.BankTransaction, store.BankAccount, error) {
	for _, p := range l.Transactions {
		if p.ID != id {
			continue
		}
		for _, batch := range l.Imports {
			if batch.ID != p.ImportID {
				continue
			}
			for _, a := range l.Accounts {
				if a.ID == batch.AccountID {
					return p, a, nil
				}
			}
		}
	}
	return store.BankTransaction{}, store.BankAccount{}, store.ErrNotFound
}

func (s *Server) handleBankReview(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	hh, ok := s.importHousehold(w, r)
	if !ok {
		return
	}
	ledger, err := s.store.BankLedger(r.Context(), hh)
	if err != nil {
		s.bankError(w, r, err)
		return
	}
	p, account, err := findBankPayment(ledger, parseID(r.PathValue("id")))
	if err != nil {
		s.bankError(w, r, err)
		return
	}
	if p.Decision == "pending" {
		p.Name, p.Reference = strings.TrimSpace(r.FormValue("name")), strings.TrimSpace(r.FormValue("reference"))
		p.BookedOn, p.Direction = r.FormValue("date"), store.Direction(r.FormValue("direction"))
		if strings.TrimSpace(r.FormValue("amount")) == "" {
			s.bankError(w, r, store.ErrInvalid)
			return
		}
		p.AmountCents, err = ParseCents(r.FormValue("amount"))
		if err != nil {
			s.bankError(w, r, store.ErrInvalid)
			return
		}
		p.CategoryID, p.PlanID = parseID(r.FormValue("category")), parseID(r.FormValue("plan"))
		p.CostNature, p.BudgetClass = store.CostNature(r.FormValue("nature")), store.BudgetClass(r.FormValue("class"))
		p.Settle = r.FormValue("settle") == "yes"
		p.TransferAccountID = parseID(r.FormValue("transfer_account"))
	}
	p.Decision = r.FormValue("decision")
	p.Splits, p.TagIDs = nil, nil
	if p.Decision == "accepted" {
		p.TransferAccountID = 0
		switch r.FormValue("allocation") {
		case "owner":
			p.Splits = []store.BankSplit{{MemberID: account.MemberID, Cents: p.AmountCents}}
		case "plan":
			data, err := s.loadHouseholdData(r.Context(), hh)
			if err != nil {
				s.bankError(w, r, err)
				return
			}
			plan, err := s.store.GetBooking(r.Context(), hh, p.PlanID)
			if err != nil {
				s.bankError(w, r, err)
				return
			}
			plan.AmountCents = calc.AmountFor(plan, data.Overrides[plan.ID], p.BookedOn[:min(7, len(p.BookedOn))])
			p.Splits = calc.ActualAllocation(plan, data.Splits[plan.ID], p.AmountCents)
			p.TagIDs = data.TagLinks[plan.ID]
			if len(p.Splits) == 0 {
				s.bankError(w, r, store.ErrInvalid)
				return
			}
		default:
			s.bankError(w, r, store.ErrInvalid)
			return
		}
	}
	err = s.store.ReviewBankTransaction(r.Context(), hh, p, r.FormValue("allow_duplicate") == "yes")
	if err != nil {
		s.bankError(w, r, err)
		return
	}
	redirectImportMonth(w, r, p.BookedOn[:7])
}

func (s *Server) handleBankMonth(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	hh, ok := s.importHousehold(w, r)
	if !ok {
		return
	}
	closed := r.FormValue("closed") == "yes"
	if closed && r.FormValue("complete") != "yes" {
		s.clientError(w, r, http.StatusBadRequest, "import.confirmComplete")
		return
	}
	month := r.FormValue("month")
	if err := s.store.SetMonthReconciled(r.Context(), hh, month, closed); err != nil {
		s.bankError(w, r, err)
		return
	}
	redirectImportMonth(w, r, month)
}

func redirectImportMonth(w http.ResponseWriter, r *http.Request, month string) {
	http.Redirect(w, r, importMonthURL(month), http.StatusSeeOther) // #nosec G710 -- fixed /imports path; importMonthURL query-escapes the month.
}

func importMonthURL(month string) string { return "/imports?m=" + url.QueryEscape(month) }

func (s *Server) handleBankPlan(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	hh, ok := s.importHousehold(w, r)
	if !ok {
		return
	}
	err := s.store.CreatePlanFromPayment(r.Context(), hh, parseID(r.PathValue("id")),
		store.Frequency(r.FormValue("frequency")), r.FormValue("start"))
	if err != nil {
		s.bankError(w, r, err)
		return
	}
	http.Redirect(w, r, "/bookings?m="+url.QueryEscape(r.FormValue("start")[:7]), http.StatusSeeOther)
}

func (s *Server) bankError(w http.ResponseWriter, r *http.Request, err error) {
	var tableErr *bankimport.TableError
	if errors.As(err, &tableErr) {
		s.logger.Warn("structured statement rejected", "reason", tableErr.Kind, "row", tableErr.Row)
		message := T(r.Context(), tableErrorKey(tableErr.Kind))
		if tableErr.Row > 0 {
			message = Tf(r.Context(), "import.tableRow", tableErr.Row) + " " + message
		}
		http.Error(w, message, http.StatusBadRequest)
		return
	}
	status, key := http.StatusBadRequest, i18n.Key("import.invalid")
	switch {
	case errors.Is(err, store.ErrNotFound):
		status, key = http.StatusNotFound, "import.notFound"
	case errors.Is(err, store.ErrDuplicateImport):
		status, key = http.StatusConflict, "import.duplicateFile"
	case errors.Is(err, store.ErrDuplicatePayment):
		status, key = http.StatusConflict, "import.duplicatePayment"
	case errors.Is(err, store.ErrClosedMonth):
		status, key = http.StatusConflict, "import.closedError"
	case errors.Is(err, store.ErrPendingPayments):
		status, key = http.StatusConflict, "import.pendingError"
	case errors.Is(err, store.ErrBankHistoryInUse):
		status, key = http.StatusConflict, "import.historyInUse"
	case errors.Is(err, store.ErrDefaultBankAccount):
		status, key = http.StatusConflict, "settings.defaultAccountProtected"
	case errors.Is(err, store.ErrBankAccountName):
		status, key = http.StatusConflict, "settings.accountNameInUse"
	case errors.Is(err, bankimport.ErrPDF):
		key = "import.pdfError"
	case errors.Is(err, bankimport.ErrFile):
		key = "import.fileError"
	case errors.Is(err, bankimport.ErrSheet):
		key = "import.sheetError"
	case errors.Is(err, bankimport.ErrConfiguration):
		status, key = http.StatusServiceUnavailable, "import.configureModel"
	case errors.Is(err, errImportPreview):
		status, key = http.StatusConflict, "import.previewExpired"
	case errors.Is(err, errImportPreviewFull):
		status, key = http.StatusTooManyRequests, "import.previewFull"
	case errors.Is(err, errImportMapping), errors.Is(err, bankimport.ErrAccountMapping):
		key = "import.mappingTargets"
	case errors.Is(err, errUploadContext):
		status, key = http.StatusConflict, "import.uploadContextChanged"
	case errors.Is(err, bankimport.ErrNoText):
		key = "import.noText"
	case errors.Is(err, bankimport.ErrResult):
		key = "import.resultError"
	case errors.Is(err, bankimport.ErrDeployment):
		key = "import.deploymentError"
	case errors.Is(err, bankimport.ErrUpstream), errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		status, key = http.StatusBadGateway, "import.aiError"
	case !errors.Is(err, store.ErrInvalid):
		s.serverError(w, r, err)
		return
	}
	s.logger.Warn("bank import request rejected", "reason", key)
	s.clientError(w, r, status, key)
}
