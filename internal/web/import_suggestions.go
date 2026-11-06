package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/daknoblo/Haushaltsbuch/internal/calc"
	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

func matchText(text string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }), " ")
}

func containsPhrase(text, phrase string) bool {
	return phrase != "" && strings.Contains(" "+matchText(text)+" ", " "+matchText(phrase)+" ")
}

func historyKey(p store.BankTransaction) string {
	key := matchText(p.Name)
	for _, processor := range []string{"paypal", "klarna", "stripe", "sumup", "amazon", "ebay", "etsy"} {
		if containsPhrase(p.Name, processor) {
			if matchText(p.Reference) == "" {
				return key + "\x00payment:" + strconv.FormatInt(p.ID, 10)
			}
			return key + "\x00" + matchText(p.Reference)
		}
	}
	return key
}

func sameHistory(a, b store.BankTransaction) bool {
	if a.CategoryID != b.CategoryID || a.CostNature != b.CostNature || a.BudgetClass != b.BudgetClass || a.Settle != b.Settle || len(a.Splits) != len(b.Splits) {
		return false
	}
	tagsA, tagsB := slices.Clone(a.TagIDs), slices.Clone(b.TagIDs)
	slices.Sort(tagsA)
	slices.Sort(tagsB)
	if !slices.Equal(tagsA, tagsB) {
		return false
	}
	for _, left := range a.Splits {
		found := false
		for _, right := range b.Splits {
			if left.MemberID != right.MemberID {
				continue
			}
			found = len(a.Splits) == 1 || big.NewRat(left.Cents, max(a.AmountCents, 1)).Cmp(big.NewRat(right.Cents, max(b.AmountCents, 1))) == 0
		}
		if !found {
			return false
		}
	}
	return true
}

func historyAllocation(source store.BankTransaction, amount int64) []store.BankSplit {
	if len(source.Splits) == 1 {
		return []store.BankSplit{{MemberID: source.Splits[0].MemberID, Cents: amount}}
	}
	splits := make([]store.BookingSplit, 0, len(source.Splits))
	for _, split := range source.Splits {
		splits = append(splits, store.BookingSplit{MemberID: split.MemberID, Value: float64(split.Cents)})
	}
	return calc.ActualAllocation(store.Booking{AmountCents: source.AmountCents, SplitMode: store.SplitFixed}, splits, amount)
}

func proposeImport(row *ImportRow, data calc.Data, ledger store.BankLedger, account store.BankAccount) {
	p := &row.Payment
	row.Allocation = "owner"
	if p.Decision != "pending" {
		return
	}
	hasDraft := len(p.Splits) > 0 && p.PlanID == 0
	if hasDraft {
		row.Allocation = "draft"
	}
	if p.PlanID != 0 {
		row.Suggestion = "plan"
	} else if p.CategoryID != 0 {
		row.Suggestion = "manual"
	}
	p.Name, p.Reference = strings.TrimSpace(p.Name), strings.TrimSpace(p.Reference)
	if p.CategoryID == 0 && p.PlanID == 0 && !hasDraft {
		nature, class, settle := p.CostNature, p.BudgetClass, p.Settle
		suggestPlan(p, data)
		if nature != store.CostVariable || class != store.ClassNeed {
			p.CostNature, p.BudgetClass = nature, class
		}
		if !settle {
			p.Settle = false
		}
		if p.PlanID != 0 {
			row.Suggestion = "plan"
		}
		if p.CategoryID == 0 {
			batches := map[int64]int64{}
			for _, batch := range ledger.Imports {
				batches[batch.ID] = batch.AccountID
			}
			var previous *store.BankTransaction
			conflict := false
			for _, candidate := range ledger.Transactions {
				if candidate.Decision != "accepted" || batches[candidate.ImportID] != account.ID || candidate.Direction != p.Direction || historyKey(candidate) != historyKey(*p) {
					continue
				}
				if previous != nil && !sameHistory(*previous, candidate) {
					conflict = true
					break
				}
				if previous == nil || candidate.BookedOn > previous.BookedOn || (candidate.BookedOn == previous.BookedOn && candidate.ID > previous.ID) {
					matched := candidate
					previous = &matched
				}
			}
			switch {
			case conflict:
				row.Suggestion = "conflict"
			case previous != nil:
				p.CategoryID = previous.CategoryID
				if p.CostNature == store.CostVariable && p.BudgetClass == store.ClassNeed {
					p.CostNature, p.BudgetClass = previous.CostNature, previous.BudgetClass
				}
				p.Settle = p.Settle && previous.Settle
				p.TagIDs = slices.Clone(previous.TagIDs)
				p.Splits = historyAllocation(*previous, p.AmountCents)
				row.Allocation, row.HistoryID, row.Suggestion = "history", previous.ID, "history"
				if len(previous.Splits) == 1 && previous.Splits[0].MemberID == account.MemberID && len(previous.TagIDs) == 0 {
					row.Allocation = "owner"
				}
			default:
				suggestCategory(row, data, account)
			}
		}
	}
	if p.PlanID != 0 {
		row.Allocation = "plan"
		for _, plan := range data.Bookings {
			if plan.ID != p.PlanID {
				continue
			}
			plan.AmountCents = calc.AmountFor(plan, data.Overrides[plan.ID], p.BookedOn[:7])
			p.Splits = calc.ActualAllocation(plan, data.Splits[plan.ID], p.AmountCents)
			p.TagIDs = slices.Clone(data.TagLinks[plan.ID])
		}
	} else if row.Allocation == "owner" {
		p.Splits = []store.BankSplit{{MemberID: account.MemberID, Cents: p.AmountCents}}
	}
	slices.SortFunc(p.Splits, func(a, b store.BankSplit) int {
		if a.MemberID < b.MemberID {
			return -1
		}
		if a.MemberID > b.MemberID {
			return 1
		}
		return 0
	})
	slices.Sort(p.TagIDs)
}

func suggestCategory(row *ImportRow, data calc.Data, account store.BankAccount) {
	p := &row.Payment
	text := p.Name + " " + p.Reference
	matches := map[int64]bool{}
	for _, category := range data.Categories {
		if category.Classification != p.Direction {
			continue
		}
		if containsPhrase(text, category.Name) {
			matches[category.ID] = true
		}
	}
	nature, class := p.CostNature, p.BudgetClass
	for _, rule := range []struct {
		categories, keywords []string
		nature               store.CostNature
		class                store.BudgetClass
	}{
		{[]string{"Lebensmittel", "Groceries"}, []string{"rewe", "edeka", "aldi", "lidl", "netto", "kaufland", "backhaus", "bäckerei", "baeckerei"}, store.CostVariable, store.ClassNeed},
		{[]string{"Abo", "Abos", "Abonnements", "Subscriptions"}, []string{"netflix", "spotify", "disney plus", "youtube premium"}, store.CostFix, store.ClassWant},
		{[]string{"Mobilität", "Transport", "Verkehr"}, []string{"deutsche bahn", "db vertrieb", "uber"}, store.CostVariable, store.ClassNeed},
		{[]string{"Drogerie", "Drugstore"}, []string{"dm", "rossmann", "müller drogerie"}, store.CostVariable, store.ClassNeed},
		{[]string{"Gehalt", "Salary"}, []string{"gehalt", "lohn", "salary", "payroll"}, store.CostVariable, store.ClassNeed},
	} {
		if !slices.ContainsFunc(rule.keywords, func(key string) bool {
			return containsPhrase(text, key) && (key != "uber" || !containsPhrase(text, "uber eats"))
		}) {
			continue
		}
		for _, category := range data.Categories {
			if category.Classification != p.Direction {
				continue
			}
			if slices.ContainsFunc(rule.categories, func(name string) bool { return matchText(name) == matchText(category.Name) }) {
				matches[category.ID] = true
				nature, class = rule.nature, rule.class
				row.RuleClassification = true
			}
		}
	}
	source := "rule"
	if len(matches) == 0 {
		source = "account"
		for _, category := range data.Categories {
			if category.Classification == p.Direction && containsPhrase(account.Name, category.Name) {
				matches[category.ID] = true
			}
		}
	}
	if len(matches) != 1 {
		return
	}
	for id := range matches {
		p.CategoryID = id
	}
	if p.CostNature == store.CostVariable && p.BudgetClass == store.ClassNeed {
		p.CostNature, p.BudgetClass = nature, class
	}
	row.Suggestion = source
}

func describeImport(ctx context.Context, row *ImportRow, data calc.Data) {
	for _, category := range data.Categories {
		if category.ID == row.Payment.CategoryID && category.Classification == row.Payment.Direction {
			row.Category = category.Name
			row.CategoryColor = category.Color
		}
	}
	for _, tagID := range row.Payment.TagIDs {
		for _, tag := range data.Tags {
			if tag.ID == tagID {
				row.Tags = append(row.Tags, tag)
			}
		}
	}
	var parts []string
	for _, split := range row.Payment.Splits {
		for _, member := range data.Members {
			if member.ID == split.MemberID {
				parts = append(parts, member.Name+": "+FormatEUR(split.Cents))
			}
		}
	}
	row.AllocationLabel = strings.Join(parts, " · ")
	row.CanQuick = row.Payment.Decision == "pending" && !row.Closed && !row.Duplicate && row.TransferCandidate == "" && row.Payment.TransferAccountID == 0 &&
		row.Category != "" && len(row.Payment.Splits) > 0 && row.AllocationLabel != "" && row.Suggestion != "conflict"
	row.Stage = "needs"
	switch {
	case row.Payment.Decision != "pending":
		row.Stage = "done"
	case row.CanQuick:
		row.Stage = "ready"
		if row.Suggestion == "account" || row.Suggestion == "ai" || row.SimilarCandidate != "" ||
			row.PlanOwnerMismatch || (row.PlanName != "" && row.PlanAmount != row.Payment.AmountCents) {
			row.Stage = "suggested"
		}
	}
	classificationInferred := row.Suggestion == "plan" || row.Suggestion == "history" || (row.Suggestion == "rule" && row.RuleClassification)
	row.NatureDefault = row.Payment.Decision == "pending" && row.Payment.CostNature == store.CostVariable && !classificationInferred
	row.ClassDefault = row.Payment.Decision == "pending" && row.Payment.BudgetClass == store.ClassNeed && !classificationInferred
	if row.Suggestion == "ai" && row.Payment.Analysis != nil {
		row.NatureDefault = row.Payment.Analysis.CostNature == ""
		row.ClassDefault = row.Payment.Analysis.BudgetClass == ""
	}
	row.AllocationDefault = row.Payment.Decision == "pending" && row.Allocation == "owner" && row.Suggestion != "history"
	if row.AllocationLabel == "" && row.Payment.Decision == "pending" {
		row.AllocationLabel = T(ctx, "import.allocationMissing")
	}
}

// ReviewToken binds direct acceptance to the data and settings shown in the row.
func (row ImportRow) ReviewToken() (string, error) {
	body, err := json.Marshal(struct {
		Original, Proposed                      store.BankTransaction
		AccountID                               int64
		Category, Account, Allocation, PlanName string
		PlanAmount                              int64
		Tags                                    []store.Tag
	}{row.original, row.Payment, row.AccountID, row.Category, row.Account, row.AllocationLabel, row.PlanName, row.PlanAmount, row.Tags})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

// ID anchors an individual review update without replacing neighboring edits.
func (row ImportRow) ID() string { return "bank-row-" + strconv.FormatInt(row.Payment.ID, 10) }

// FormID associates the row's direct action with its detailed review form.
func (row ImportRow) FormID() string { return "bank-review-" + strconv.FormatInt(row.Payment.ID, 10) }

// ImportSuggestionLabel explains the local source, not a guarantee of accuracy.
func ImportSuggestionLabel(ctx context.Context, source string) string {
	switch source {
	case "plan":
		return T(ctx, "import.suggestionPlan")
	case "history":
		return T(ctx, "import.suggestionHistory")
	case "rule":
		return T(ctx, "import.suggestionRule")
	case "conflict":
		return T(ctx, "import.suggestionConflict")
	case "account":
		return T(ctx, "review.sourceAccount")
	case "manual":
		return T(ctx, "review.sourceManual")
	case "ai":
		return T(ctx, "review.sourceAI")
	default:
		return ""
	}
}
