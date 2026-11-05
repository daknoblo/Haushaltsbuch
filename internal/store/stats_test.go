package store

import (
	"strings"
	"testing"
)

func TestStatsIncludesActualCategoryAndTagUsage(t *testing.T) {
	st, household, account, payment := bankFixture(t)
	ctx := t.Context()
	tag, err := st.CreateTag(ctx, household, "Actual only", "#123456")
	if err != nil {
		t.Fatal(err)
	}
	before, err := st.Stats(ctx, household)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateBankImport(ctx, household, BankImport{AccountID: account.ID, Digest: strings.Repeat("c", 64), Filename: "synthetic.csv"}, []BankTransaction{payment}); err != nil {
		t.Fatal(err)
	}
	ledger, err := st.BankLedger(ctx, household)
	if err != nil {
		t.Fatal(err)
	}
	payment = ledger.Transactions[0]
	payment.Decision = "accepted"
	payment.Splits = []BankSplit{{MemberID: account.MemberID, Cents: payment.AmountCents}}
	payment.TagIDs = []int64{tag.ID}
	if err := st.ReviewBankTransaction(ctx, household, payment, false); err != nil {
		t.Fatal(err)
	}
	after, err := st.Stats(ctx, household)
	if err != nil {
		t.Fatal(err)
	}
	if after.Bookings != before.Bookings || after.UnusedCategories != before.UnusedCategories-1 || after.UnusedTags != before.UnusedTags-1 {
		t.Fatalf("actuals changed plan counts or were treated as unused: before=%+v after=%+v", before, after)
	}
	snapshot, err := st.ReadHousehold(ctx, household)
	if err != nil || snapshot.Household.ID != household || len(snapshot.Bank.Transactions) != 1 {
		t.Fatalf("snapshot: %+v %v", snapshot, err)
	}
}
