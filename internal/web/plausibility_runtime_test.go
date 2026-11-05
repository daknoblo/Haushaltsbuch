package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/daknoblo/Haushaltsbuch/internal/calc"
)

func TestPlausibilityPageUsesReadOnlyHouseholdSnapshot(t *testing.T) {
	srv, handler, household, _ := plausibilityFixture(t)
	before, err := srv.store.ReadHousehold(t.Context(), household.ID)
	if err != nil {
		t.Fatal(err)
	}
	landing := get(t, handler, "/settings/plausibility?year=2026")
	if landing.Code != http.StatusOK || strings.Contains(landing.Body.String(), "data-plausibility-result") {
		t.Fatal("check must start explicitly")
	}
	result := get(t, handler, "/settings/plausibility?year=2026&run=1")
	if result.Code != http.StatusOK || !strings.Contains(result.Body.String(), T(t.Context(), "check.review")) || strings.Contains(result.Body.String(), T(t.Context(), "check.failed")) {
		t.Fatalf("check failed: %s", result.Body.String())
	}
	if result.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("check can be cached")
	}
	after, err := srv.store.ReadHousehold(t.Context(), household.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("check wrote household data")
	}
	if get(t, handler, "/settings/plausibility?year=wrong&run=1").Code != http.StatusBadRequest {
		t.Fatal("invalid year accepted")
	}
	if !strings.Contains(get(t, handler, "/settings").Body.String(), `href="/settings/plausibility"`) {
		t.Fatal("settings entry missing")
	}
}

func TestPlausibilityDetectsWrongRenderedTextAndMissingMetric(t *testing.T) {
	var result calc.PlausibilityResult
	component := statTile("Synthetic", "1,02 €", "", "")
	err := checkRenderedStats(t.Context(), &result, calc.Finding{Code: "render"}, component, map[string]string{"Synthetic": "1,01 €", "Missing": "0,00 €"})
	if err != nil || result.Errors != 3 {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestPlausibilityReferenceCoversUnclampedAggregates(t *testing.T) {
	value := int64(4_333_333_333_333)
	want := "43.333.333.333,33 €"
	if got := FormatEUR(value); got != want {
		t.Fatalf("valid aggregate was truncated: %s", got)
	}
	var result calc.PlausibilityResult
	if err := checkRenderedStats(t.Context(), &result, calc.Finding{Code: "render"}, statTile("Total", FormatEUR(value), "", ""), map[string]string{"Total": referenceMoney(value)}); err != nil || result.Errors != 0 {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestPlausibilityDisplayCheckCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	component := templ.ComponentFunc(func(ctx context.Context, _ io.Writer) error { return ctx.Err() })
	var result calc.PlausibilityResult
	err := checkRenderedStats(ctx, &result, calc.Finding{}, component, map[string]string{"Total": "0"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled render reported success: %v", err)
	}
}
