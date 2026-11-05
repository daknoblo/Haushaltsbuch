package calc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

func checkSource(ctx context.Context, out *PlausibilityResult, s store.HouseholdSnapshot) error {
	members, categories, tags, plans := map[int64]bool{}, map[int64]store.Direction{}, map[int64]bool{}, map[int64]bool{}
	check := func(valid bool, code, name string) {
		out.Checks++
		if !valid {
			out.add(Finding{Code: code, Name: name})
		}
	}
	for _, m := range s.Members {
		check(m.ID > 0 && !members[m.ID] && m.HouseholdID == s.Household.ID && strings.TrimSpace(m.Name) != "", "data", m.Name)
		members[m.ID] = true
	}
	for _, c := range s.Categories {
		check(c.ID > 0 && categories[c.ID] == "" && c.HouseholdID == s.Household.ID && c.Classification.Valid(), "data", c.Name)
		categories[c.ID] = c.Classification
	}
	for _, tag := range s.Tags {
		check(tag.ID > 0 && !tags[tag.ID] && tag.HouseholdID == s.Household.ID, "data", tag.Name)
		tags[tag.ID] = true
	}
	checkTags := func(ids []int64, name string) {
		seen := map[int64]bool{}
		for _, id := range ids {
			check(tags[id] && !seen[id], "reference", name)
			seen[id] = true
		}
	}
	duplicates := map[string]bool{}
	type planRange struct{ from, until string }
	recurringRanges := map[string][]planRange{}
	for _, row := range s.Bookings {
		if err := ctx.Err(); err != nil {
			return err
		}
		b := row.Booking
		check(b.ID > 0 && !plans[b.ID] && b.HouseholdID == s.Household.ID, "data", b.Name)
		plans[b.ID] = true
		check(b.Direction.Valid() && b.Frequency.Valid() && b.SplitMode.Valid() &&
			(b.Direction == store.DirIncome || (b.CostNature.Valid() && b.BudgetClass.Valid())) &&
			b.Interval >= 1 && b.Interval <= 60 && b.AmountCents >= 0 && b.AmountCents <= MaxSplitCents, "data", b.Name)
		check(categories[b.CategoryID] == b.Direction && b.CategoryID != 0, "reference", b.Name)
		check(store.ValidateDateRange(b.StartsOn, b.EndsOn) == nil && (b.Frequency != store.FreqOnce || b.StartsOn != ""), "date", b.Name)
		if b.PayerMemberID != nil {
			check(members[*b.PayerMemberID], "reference", b.Name)
		}
		if strings.TrimSpace(b.Name) == "" {
			out.add(Finding{Warning: true, Code: "draft"})
		}
		seen := map[int64]bool{}
		sum := new(big.Rat)
		for _, sp := range row.Splits {
			check(members[sp.MemberID] && !seen[sp.MemberID] && sp.BookingID == b.ID, "reference", b.Name)
			check(!math.IsNaN(sp.Value) && !math.IsInf(sp.Value, 0) && sp.Value >= 0 && sp.Value <= MaxSplitCents && (b.SplitMode != store.SplitPercent || sp.Value <= 100), "data", b.Name)
			seen[sp.MemberID] = true
			if !math.IsNaN(sp.Value) && !math.IsInf(sp.Value, 0) {
				sum.Add(sum, referenceDecimal(sp.Value))
			}
		}
		incomplete := len(row.Splits) == 0 || (b.SplitMode == store.SplitPercent && sum.Cmp(big.NewRat(100, 1)) != 0) || (b.SplitMode == store.SplitFixed && sum.Cmp(big.NewRat(b.AmountCents, 1)) != 0)
		if incomplete {
			out.add(Finding{Warning: true, Code: "incomplete", Name: b.Name})
		}
		if b.Settle && b.Direction == store.DirExpense && b.PayerMemberID == nil {
			out.add(Finding{Warning: true, Code: "payer", Name: b.Name})
		}
		for _, o := range row.Overrides {
			check(o.BookingID == b.ID && o.AmountCents >= 0 && o.AmountCents <= MaxSplitCents, "data", b.Name)
			check(store.ValidateDateRange(o.StartsOn, o.EndsOn) == nil, "date", b.Name)
		}
		checkTags(row.TagIDs, b.Name)
		payer := int64(0)
		if b.PayerMemberID != nil {
			payer = *b.PayerMemberID
		}
		key := fmt.Sprintf("%s|%s|%d|%d|%s|%d|%s|%s|%d|%s", strings.Join(strings.Fields(strings.ToLower(b.Name)), " "), b.Direction, b.CategoryID, b.AmountCents, b.Frequency, b.Interval, b.StartsOn, b.EndsOn, payer, b.SplitMode)
		if b.Name != "" && duplicates[key] {
			out.add(Finding{Warning: true, Code: "duplicatePlan", Name: b.Name})
		}
		if b.Name != "" && b.Frequency.Recurring() && store.ValidateDateRange(b.StartsOn, b.EndsOn) == nil {
			span := planRange{from: "0001-01", until: "9999-12"}
			if b.StartsOn != "" {
				span.from = b.StartsOn[:7]
			}
			if b.EndsOn != "" {
				span.until = b.EndsOn[:7]
			}
			group := fmt.Sprintf("%s|%s|%d|%d|%s|%d", strings.Join(strings.Fields(strings.ToLower(b.Name)), " "), b.Direction, b.CategoryID, payer, b.Frequency, b.Interval)
			if !duplicates[key] {
				for _, previous := range recurringRanges[group] {
					if span.from <= previous.until && previous.from <= span.until {
						out.add(Finding{Warning: true, Code: "overlapPlan", Name: b.Name, Month: max(span.from, previous.from)})
						break
					}
				}
			}
			recurringRanges[group] = append(recurringRanges[group], span)
		}
		duplicates[key] = true
	}
	accounts, imports := map[int64]int64{}, map[int64]int64{}
	defaults := map[int64]int{}
	for _, a := range s.Bank.Accounts {
		check(a.ID > 0 && accounts[a.ID] == 0 && members[a.MemberID], "reference", a.Name)
		accounts[a.ID] = a.MemberID
		if a.Default {
			defaults[a.MemberID]++
		}
	}
	for _, m := range s.Members {
		check(defaults[m.ID] == 1, "defaultAccount", m.Name)
	}
	digests := map[string]bool{}
	for _, batch := range s.Bank.Imports {
		check(batch.ID > 0 && imports[batch.ID] == 0 && accounts[batch.AccountID] != 0, "reference", batch.Filename)
		imports[batch.ID] = batch.AccountID
		key := fmt.Sprintf("%d:%s", batch.AccountID, batch.Digest)
		check(batch.Digest != "" && !digests[key], "data", batch.Filename)
		digests[key] = true
	}
	closed := map[string]bool{}
	for _, month := range s.Bank.ClosedMonths {
		_, err := time.Parse("2006-01", month)
		check(err == nil && !closed[month], "date", month)
		closed[month] = true
	}
	payments := map[int64]bool{}
	duplicates = map[string]bool{}
	for _, p := range s.Bank.Transactions {
		if err := ctx.Err(); err != nil {
			return err
		}
		check(p.ID > 0 && !payments[p.ID] && imports[p.ImportID] != 0, "reference", p.Name)
		payments[p.ID] = true
		check(store.ValidateBankTransaction(p) == nil, "data", p.Name)
		if p.PlanID != 0 {
			check(plans[p.PlanID], "reference", p.Name)
		}
		if p.CategoryID != 0 {
			check(categories[p.CategoryID] == p.Direction, "reference", p.Name)
		}
		check(p.Fingerprint == store.PaymentFingerprint(p), "fingerprint", p.Name)
		if p.SourceFingerprint != "" {
			key, err := hex.DecodeString(p.SourceFingerprint)
			check(err == nil && len(key) == sha256.Size, "fingerprint", p.Name)
		}
		if len(p.BookedOn) >= 7 && closed[p.BookedOn[:7]] {
			check(p.Decision != "pending", "closedPending", p.Name)
		}
		if p.Decision == "transfer" {
			check(accounts[p.TransferAccountID] != 0 && p.TransferAccountID != imports[p.ImportID], "reference", p.Name)
		}
		if p.Decision != "accepted" {
			check(len(p.Splits) == 0 && len(p.TagIDs) == 0, "excluded", p.Name)
			continue
		}
		check(p.CategoryID != 0 && p.TransferAccountID == 0, "reference", p.Name)
		var sum int64
		seen := map[int64]bool{}
		for _, sp := range p.Splits {
			check(members[sp.MemberID] && !seen[sp.MemberID] && sp.Cents >= 0 && sp.Cents <= MaxSplitCents, "reference", p.Name)
			seen[sp.MemberID] = true
			sum += sp.Cents
		}
		check(len(p.Splits) > 0 && sum == p.AmountCents, "paymentAllocation", p.Name)
		checkTags(p.TagIDs, p.Name)
		key := fmt.Sprintf("%d:%s", imports[p.ImportID], store.PaymentFingerprint(p))
		if duplicates[key] {
			month := ""
			if len(p.BookedOn) >= 7 {
				month = p.BookedOn[:7]
			}
			out.add(Finding{Warning: true, Code: "duplicatePayment", Name: p.Name, Month: month})
		}
		duplicates[key] = true
	}
	return nil
}
