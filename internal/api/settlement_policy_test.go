package api

import (
	"net/http"
	"strconv"
	"testing"
)

func TestBookingEffectiveSettlementKeepsManualPreference(t *testing.T) {
	st, h, hh := newTestAPI(t)
	members, err := st.ListMembers(t.Context(), hh.ID)
	if err != nil {
		t.Fatal(err)
	}
	sole := []map[string]any{{"member": members[0].ID}}
	shared := []map[string]any{{"member": members[0].ID}, {"member": members[1].ID}}
	w := do(t, h, http.MethodPost, "/api/v1/bookings", token, map[string]any{
		"name": "Own cost", "category": "Miete", "amount": 1000,
		"payer_id": members[0].ID, "shares": sole, "settle": true,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", w.Code, w.Body.String())
	}
	b := decodeBody[bookingOut](t, w)
	if !b.Settle || b.SettleEffective {
		t.Fatalf("saved preference/effective setting = %+v", b)
	}
	path := "/api/v1/bookings/" + strconv.FormatInt(b.ID, 10)
	for _, tc := range []struct {
		body      map[string]any
		requested bool
		effective bool
	}{
		{map[string]any{"note": "still alone"}, true, false},
		{map[string]any{"shares": shared}, true, true},
		{map[string]any{"settle": false}, false, false},
		{map[string]any{"shares": sole}, false, false},
		{map[string]any{"shares": shared}, false, false},
	} {
		w = do(t, h, http.MethodPut, path, token, tc.body)
		if w.Code != http.StatusOK {
			t.Fatalf("update = %d: %s", w.Code, w.Body.String())
		}
		got := decodeBody[bookingOut](t, w)
		if got.Settle != tc.requested || got.SettleEffective != tc.effective {
			t.Errorf("requested %t, effective %t; want %t, %t", got.Settle, got.SettleEffective, tc.requested, tc.effective)
		}
	}
	w = do(t, h, http.MethodPut, path, token, map[string]any{"settle_effective": true})
	if w.Code != http.StatusBadRequest {
		t.Errorf("effective setting is read-only, got %d", w.Code)
	}
}
