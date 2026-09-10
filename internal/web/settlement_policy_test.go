package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/daknoblo/Haushaltsbuch/internal/calc"
	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

func TestBookingSolePayerKeepsPreferenceAcrossAutosaves(t *testing.T) {
	srv, h, hh := newTestServer(t)
	members, err := srv.store.ListMembers(t.Context(), hh.ID)
	if err != nil {
		t.Fatal(err)
	}
	b := newExpenseBooking(t, srv, hh.ID)
	b.PayerMemberID = &members[0].ID
	b.Settle = true
	b.SplitMode = store.SplitEqual
	if err := srv.store.SaveBooking(t.Context(), b, []store.SplitInput{{MemberID: members[0].ID}}, nil); err != nil {
		t.Fatal(err)
	}
	path := "/bookings/" + strconv.FormatInt(b.ID, 10)
	body := get(t, h, path+"/edit?m=2026-09").Body.String()
	if !strings.Contains(body, `data-settle-toggle disabled`) ||
		!strings.Contains(body, `name="settle" value="1" data-settle-preference`) {
		t.Fatalf("sole payer needs an unchecked, disabled switch and retained preference: %s", body)
	}
	form := url.Values{
		"name": {b.Name}, "amount": {"1200"}, "recurring": {"1"},
		"frequency": {"monthly"}, "split_mode": {"equal"}, "settle": {"1"},
		"payer_member_id": {strconv.FormatInt(members[0].ID, 10)},
		"m_" + strconv.FormatInt(members[0].ID, 10): {"1"},
	}
	check := func(want bool) {
		t.Helper()
		if w := post(t, h, path, form); w.Code != http.StatusNoContent {
			t.Fatalf("save = %d: %s", w.Code, w.Body.String())
		}
		stored, err := srv.store.GetBooking(t.Context(), hh.ID, b.ID)
		if err != nil {
			t.Fatal(err)
		}
		splits, err := srv.store.ListSplits(t.Context(), hh.ID, b.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got := calc.SettlementEnabled(stored, splits, nil, "2026-09"); got != want {
			t.Errorf("effective settlement = %t, want %t", got, want)
		}
		if stored.Settle != (form.Get("settle") == "1") {
			t.Error("automatic rule replaced the user's saved preference")
		}
	}
	check(false)
	second := "m_" + strconv.FormatInt(members[1].ID, 10)
	form.Set(second, "1")
	check(true)
	form.Set("settle", "")
	check(false)
	form.Del(second)
	check(false)
	form.Set(second, "1")
	check(false)
}
