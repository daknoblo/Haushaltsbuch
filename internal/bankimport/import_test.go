package bankimport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

const validResult = `{"currency":"EUR","complete":true,"opening_balance_cents":10000,"closing_balance_cents":8766,
	"transactions":[{"booked_on":"2020-01-15","direction":"expense","amount_cents":1234,"name":"Synthetic payment","reference":"test"}]}`

func TestParseResultStrictAndComplete(t *testing.T) {
	if rows, err := ParseResult(validResult); err != nil || len(rows) != 1 || rows[0].AmountCents != 1234 {
		t.Fatalf("valid: %+v %v", rows, err)
	}
	for _, invalid := range []string{
		strings.Replace(validResult, `"EUR"`, `"USD"`, 1),
		strings.Replace(validResult, `"complete":true`, `"complete":false`, 1),
		strings.Replace(validResult, `"amount_cents":1234,`, ``, 1),
		strings.Replace(validResult, `"amount_cents":1234`, `"amount_cents":12.34`, 1),
		strings.Replace(validResult, `"amount_cents":1234`, `"amount_cents":-1234`, 1),
		strings.Replace(validResult, `"2020-01-15"`, `"2020-02-31"`, 1),
		strings.Replace(validResult, `"closing_balance_cents":8766`, `"closing_balance_cents":8765`, 1),
		strings.Replace(validResult, `"closing_balance_cents":8766`, `"closing_balance_cents":null`, 1),
		strings.Replace(validResult, `"reference":"test"`, `"reference":"test","plan_id":1`, 1),
		validResult + `{}`, "```json\n" + validResult + "\n```",
	} {
		if _, err := ParseResult(invalid); !errors.Is(err, ErrResult) {
			t.Fatalf("accepted invalid result: %v", err)
		}
	}
}

func simplePDF(text string) []byte {
	return pdfWithPageCount(text, 1)
}

func pdfWithPageCount(text string, count int) []byte {
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	stream := "BT /F1 12 Tf 50 700 Td (" + text + ") Tj ET"
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		fmt.Sprintf("<< /Type /Pages /Kids [3 0 R] /Count %d >>", count),
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
	}
	offsets := make([]int, 0, len(objects))
	for i, object := range objects {
		offsets = append(offsets, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return out.Bytes()
}

func TestPDFTextAndBounds(t *testing.T) {
	pdf := simplePDF("Synthetic statement 12.34 EUR")
	text, err := PDFText(t.Context(), pdf)
	if err != nil || !strings.Contains(text, "Synthetic statement 12.34 EUR") {
		t.Fatalf("extraction: %q %v", text, err)
	}
	if _, err := PDFText(t.Context(), []byte("not PDF")); !errors.Is(err, ErrPDF) {
		t.Fatalf("malformed PDF: %v", err)
	}
	if _, err := PDFText(t.Context(), make([]byte, MaxPDFBytes+1)); !errors.Is(err, ErrPDF) {
		t.Fatalf("file size limit: %v", err)
	}
	tooManyPages := pdfWithPageCount("Synthetic statement", 101)
	if _, err := PDFText(t.Context(), tooManyPages); !errors.Is(err, ErrPDF) {
		t.Fatalf("page count limit: %v", err)
	}
	if _, err := PDFText(t.Context(), simplePDF("")); !errors.Is(err, ErrNoText) {
		t.Fatalf("empty PDF: %v", err)
	}
	if _, err := PDFText(t.Context(), simplePDF(strings.Repeat("x", MaxTextBytes))); !errors.Is(err, ErrPDF) {
		t.Fatalf("oversized text: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := PDFText(ctx, pdf); err == nil {
		t.Fatal("canceled extraction succeeded")
	}
}

type fixedCredential struct{}

func (fixedCredential) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "synthetic-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type transportFunc func(*http.Request) (*http.Response, error)

func (fn transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

const testResource = "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/test/providers/Microsoft.CognitiveServices/accounts/synthetic"

func TestAzureDiscoveryExtractionAndHostPinning(t *testing.T) {
	var calls int
	badNext, finish := "", "stop"
	c := &Azure{
		identity: Identity{ResourceID: testResource}, credential: fixedCredential{}, gate: make(chan struct{}, 1),
		http: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.Header.Get("Authorization") != "Bearer synthetic-token" {
				t.Fatal("missing bearer token")
			}
			var body string
			switch r.URL.Host {
			case "management.azure.com":
				if strings.HasSuffix(r.URL.Path, "/deployments") {
					body = fmt.Sprintf(`{"value":[{"id":%q,"name":"router","properties":{"provisioningState":"Succeeded","model":{"name":"model-router","format":"OpenAI"}}}],"nextLink":%q}`, testResource+"/deployments/router", badNext)
				} else {
					body = fmt.Sprintf(`{"id":%q,"properties":{"endpoint":"https://synthetic.openai.azure.com/"}}`, testResource)
				}
			case "synthetic.openai.azure.com":
				if r.URL.Path != "/openai/v1/chat/completions" || r.Method != http.MethodPost {
					t.Fatal("wrong inference route")
				}
				request, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(request), "untrusted") || !strings.Contains(string(request), `"model":"router"`) {
					t.Fatal("missing instruction or deployment")
				}
				body = fmt.Sprintf(`{"choices":[{"finish_reason":%q,"message":{"content":%q}}]}`, finish, validResult)
			default:
				t.Fatalf("credentials sent to unexpected host %s", r.URL.Host)
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		})},
	}
	rows, err := c.Extract(t.Context(), "router", "Synthetic statement text")
	if err != nil || len(rows) != 1 {
		t.Fatalf("extract: %+v %v", rows, err)
	}
	if calls != 3 {
		t.Fatalf("unexpected requests: %d", calls)
	}
	if _, err := c.Extract(t.Context(), "foreign", "test"); !errors.Is(err, ErrDeployment) {
		t.Fatalf("unlisted model: %v", err)
	}
	finish = "length"
	if _, err := c.Extract(t.Context(), "router", "test"); !errors.Is(err, ErrResult) {
		t.Fatalf("truncation accepted: %v", err)
	}
	c.refreshed = time.Time{}
	badNext = "https://attacker.invalid/deployments?api-version=2024-10-01"
	if _, err := c.Deployments(t.Context()); !errors.Is(err, ErrUpstream) {
		t.Fatalf("foreign pagination: %v", err)
	}
	if c.endpoint != "" {
		t.Fatal("failed discovery retained old endpoint")
	}
	for _, endpoint := range []string{
		"http://synthetic.openai.azure.com", "https://attacker.invalid",
		"https://synthetic.openai.azure.com.attacker.invalid", "https://user@synthetic.openai.azure.com",
		"https://synthetic.openai.azure.com/?secret=x", "https://synthetic.openai.azure.com:443",
	} {
		if verifiedEndpoint(endpoint, "synthetic", "") != "" {
			t.Fatalf("trusted %s", endpoint)
		}
	}
}

func TestIdentityIsExplicitAndOptional(t *testing.T) {
	if client, err := NewAzure(Identity{}); client != nil || err != nil {
		t.Fatal("empty identity should disable AI")
	}
	if err := (Identity{ResourceID: testResource}).Validate(); !errors.Is(err, ErrConfiguration) {
		t.Fatal("partial identity accepted")
	}
	identity := Identity{ResourceID: testResource, TenantID: "00000000-0000-0000-0000-000000000002",
		ClientID: "00000000-0000-0000-0000-000000000003", ClientSecret: "synthetic"}
	if err := identity.Validate(); err != nil {
		t.Fatal(err)
	}
}
