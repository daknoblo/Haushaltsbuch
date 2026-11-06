package bankimport

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

func classificationFixture() ClassificationRequest {
	return ClassificationRequest{Language: "de",
		Categories:   []ClassificationCategory{{ID: 10, Name: "Existing custom expense", Direction: store.DirExpense}, {ID: 20, Name: "Existing income", Direction: store.DirIncome}},
		Transactions: []ClassificationPayment{{ID: 1, Name: "Synthetic merchant", Reference: "Untrusted reference", Direction: store.DirExpense}},
	}
}

func TestClassificationIsConstrainedToExistingCategories(t *testing.T) {
	input := classificationFixture()
	valid := `{"suggestions":[{"id":1,"category_id":10,"cost_nature":"variable","budget_class":"need","reason":"Matches the supplied category."}]}`
	result, err := ParseClassification(valid, input)
	if err != nil || len(result) != 1 || result[0].CategoryID == nil || *result[0].CategoryID != 10 {
		t.Fatalf("valid classification: %+v %v", result, err)
	}
	unknown := `{"suggestions":[{"id":1,"category_id":null,"cost_nature":null,"budget_class":null,"reason":"Insufficient evidence."}]}`
	if _, err := ParseClassification(unknown, input); err != nil {
		t.Fatal("honest unknown result rejected")
	}
	for _, invalid := range []string{
		strings.Replace(valid, `"category_id":10`, `"category_id":999`, 1),
		strings.Replace(valid, `"category_id":10`, `"category_id":20`, 1),
		strings.Replace(valid, `"id":1`, `"id":2`, 1),
		strings.Replace(valid, `"id":1`, `"id":1.5`, 1),
		strings.Replace(valid, `"variable"`, `"fixed"`, 1),
		strings.Replace(valid, `"need"`, `"invented"`, 1),
		strings.Replace(valid, `"reason":`, `"amount_cents":1,"reason":`, 1),
		strings.Replace(valid, `"Matches the supplied category."`, `""`, 1),
		strings.Replace(valid, `"Matches the supplied category."`, `"`+strings.Repeat("x", 401)+`"`, 1),
		`{"suggestions":[]}`,
		valid + valid,
	} {
		if _, err := ParseClassification(invalid, input); !errors.Is(err, ErrResult) {
			t.Fatalf("invalid result accepted: %s (%v)", invalid, err)
		}
	}
	input.Transactions = append(input.Transactions, input.Transactions[0])
	if input.Valid() {
		t.Fatal("duplicate input IDs accepted")
	}
}

func TestClassificationUsesTheExistingAzureChatTransport(t *testing.T) {
	calls := 0
	client := &Azure{
		credential: fixedCredential{}, gate: make(chan struct{}, 1), endpoint: "https://synthetic.openai.azure.com/openai/v1",
		deployments: []string{"router"}, refreshed: time.Now(),
		http: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.URL.String() != "https://synthetic.openai.azure.com/openai/v1/chat/completions" || r.Method != http.MethodPost {
				t.Fatal("classification bypassed the verified inference endpoint")
			}
			var body struct {
				Model    string              `json:"model"`
				Messages []map[string]string `json:"messages"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Model != "router" || len(body.Messages) != 2 || !strings.Contains(body.Messages[0]["content"], "untrusted DATA") {
				t.Fatal("missing deployment or classification safeguards")
			}
			if strings.Contains(body.Messages[1]["content"], `"amount_cents":`) || strings.Contains(body.Messages[1]["content"], `"account_id":`) {
				t.Fatal("classification includes unnecessary financial context")
			}
			content := `{"suggestions":[{"id":1,"category_id":10,"cost_nature":null,"budget_class":null,"reason":"Existing category matches."}]}`
			wire, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": content}}}})
			if err != nil {
				t.Fatal(err)
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(wire)))}, nil
		})},
	}
	if _, err := client.Classify(t.Context(), "router", classificationFixture()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}
	if _, err := client.Classify(t.Context(), "unknown", classificationFixture()); !errors.Is(err, ErrDeployment) || calls != 1 {
		t.Fatal("unlisted model reached inference")
	}
}
