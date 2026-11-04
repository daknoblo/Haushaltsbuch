package bankimport

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

// Identity selects one Azure resource and one explicit application credential.
type Identity struct {
	ResourceID   string
	TenantID     string
	ClientID     string
	ClientSecret string
}

// Enabled distinguishes disabled configuration from a partially entered identity.
func (i Identity) Enabled() bool {
	return i.ResourceID != "" || i.TenantID != "" || i.ClientID != "" || i.ClientSecret != ""
}

var (
	uuid    = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	segment = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,89}$`)
)

// Validate rejects partial identities and malformed resource identifiers.
func (i Identity) Validate() error {
	if !i.Enabled() {
		return nil
	}
	p := strings.Split(i.ResourceID, "/")
	if len(p) != 9 || !strings.EqualFold(p[1], "subscriptions") || !uuid.MatchString(p[2]) ||
		!strings.EqualFold(p[3], "resourceGroups") || !segment.MatchString(p[4]) ||
		!strings.EqualFold(p[5], "providers") || !strings.EqualFold(p[6], "Microsoft.CognitiveServices") ||
		!strings.EqualFold(p[7], "accounts") || !segment.MatchString(p[8]) ||
		!uuid.MatchString(i.TenantID) || !uuid.MatchString(i.ClientID) || strings.TrimSpace(i.ClientSecret) == "" {
		return ErrConfiguration
	}
	return nil
}

// Azure retains the SDK token cache and a short-lived verified deployment list.
type Azure struct {
	identity    Identity
	credential  azcore.TokenCredential
	http        *http.Client
	gate        chan struct{}
	mu          sync.RWMutex
	endpoint    string
	deployments []string
	refreshed   time.Time
}

// NewAzure returns nil when the optional integration is disabled.
func NewAzure(i Identity) (*Azure, error) {
	if err := i.Validate(); err != nil {
		return nil, err
	}
	if !i.Enabled() {
		return nil, nil
	}
	client := &http.Client{Timeout: 150 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	cred, err := azidentity.NewClientSecretCredential(i.TenantID, i.ClientID, i.ClientSecret,
		&azidentity.ClientSecretCredentialOptions{
			DisableInstanceDiscovery: true,
			ClientOptions: azcore.ClientOptions{
				Transport: identityTransport{client: client, tenant: i.TenantID},
				Retry:     policy.RetryOptions{MaxRetries: 2, TryTimeout: 10 * time.Second},
			},
		})
	if err != nil {
		return nil, ErrConfiguration
	}
	return &Azure{identity: i, credential: cred, http: client, gate: make(chan struct{}, 1)}, nil
}

// Keep client credentials on this tenant's official token and discovery paths.
type identityTransport struct {
	client *http.Client
	tenant string
}

func (t identityTransport) Do(r *http.Request) (*http.Response, error) {
	u := r.URL
	if u.Scheme != "https" || u.Host != "login.microsoftonline.com" || u.User != nil ||
		(r.Host != "" && r.Host != u.Host) ||
		(u.Path != "/"+t.tenant+"/oauth2/v2.0/token" && u.Path != "/"+t.tenant+"/v2.0/.well-known/openid-configuration") {
		return nil, ErrConfiguration
	}
	resp, err := t.client.Do(r) // #nosec G704 -- HTTPS host and tenant-specific paths are pinned above; redirects are refused.
	if err != nil {
		return nil, ErrUpstream
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		_ = resp.Body.Close()
		return nil, ErrUpstream
	}
	body, err := boundedBody(resp)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp, nil
}

func boundedBody(resp *http.Response) ([]byte, error) {
	const maxBytes = 4 << 20
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil || len(b) > maxBytes {
		return nil, ErrUpstream
	}
	return b, nil
}

func (c *Azure) request(ctx context.Context, method, target, scope string, payload []byte, out any) error {
	token, err := c.credential.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{scope}})
	if err != nil {
		return ErrUpstream
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(payload))
	if err != nil {
		return ErrUpstream
	}
	req.Header.Set("Authorization", "Bearer "+token.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return ErrUpstream
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := boundedBody(resp)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ErrUpstream
	}
	if err := json.Unmarshal(body, out); err != nil {
		return ErrUpstream
	}
	return nil
}

func verifiedEndpoint(raw, account, custom string) string {
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" ||
		u.ForceQuery || u.Fragment != "" || u.Opaque != "" || u.Port() != "" ||
		(u.Path != "" && u.Path != "/openai/v1") {
		return ""
	}
	for _, name := range []string{account, custom} {
		if !segment.MatchString(name) {
			continue
		}
		for _, suffix := range []string{".openai.azure.com", ".cognitiveservices.azure.com", ".services.ai.azure.com"} {
			if strings.EqualFold(u.Host, name+suffix) {
				return "https://" + strings.ToLower(u.Host) + "/openai/v1"
			}
		}
	}
	return ""
}

// Deployments discovers chat deployments without accepting caller-supplied hosts.
func (c *Azure) Deployments(ctx context.Context) ([]string, error) {
	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	c.mu.RLock()
	if time.Since(c.refreshed) < 5*time.Minute {
		names := slices.Clone(c.deployments)
		c.mu.RUnlock()
		return names, nil
	}
	c.mu.RUnlock()
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	// Failed refreshes invalidate trust rather than retaining stale endpoints.
	c.mu.Lock()
	c.endpoint, c.deployments, c.refreshed = "", nil, time.Time{}
	c.mu.Unlock()
	const arm = "https://management.azure.com"
	const scope = arm + "/.default"
	const api = "?api-version=2024-10-01"
	var account struct {
		ID         string `json:"id"`
		Properties struct {
			Endpoint  string            `json:"endpoint"`
			Endpoints map[string]string `json:"endpoints"`
			Custom    string            `json:"customSubDomainName"`
		} `json:"properties"`
	}
	if err := c.request(ctx, http.MethodGet, arm+c.identity.ResourceID+api, scope, nil, &account); err != nil {
		return nil, err
	}
	if !strings.EqualFold(account.ID, c.identity.ResourceID) {
		return nil, ErrUpstream
	}
	parts := strings.Split(c.identity.ResourceID, "/")
	name := parts[len(parts)-1]
	endpoint := ""
	for _, raw := range []string{account.Properties.Endpoints["Azure OpenAI Legacy API - Latest moniker"], account.Properties.Endpoint} {
		endpoint = verifiedEndpoint(raw, name, account.Properties.Custom)
		if endpoint != "" {
			break
		}
	}
	if endpoint == "" {
		return nil, ErrUpstream
	}
	next := arm + c.identity.ResourceID + "/deployments" + api
	var names []string
	seen := make(map[string]bool)
	for page := 0; next != ""; page++ {
		u, err := url.Parse(next)
		if err != nil || page >= 20 || u.Scheme != "https" || u.Host != "management.azure.com" ||
			u.User != nil || u.Fragment != "" || u.Opaque != "" ||
			!strings.EqualFold(u.Path, c.identity.ResourceID+"/deployments") ||
			strings.Contains(u.EscapedPath(), "%") || u.Query().Get("api-version") != "2024-10-01" {
			return nil, ErrUpstream
		}
		var response struct {
			Value *[]struct {
				ID   string `json:"id"`
				Name string `json:"name"`
				SKU  struct {
					Name string `json:"name"`
				} `json:"sku"`
				Properties struct {
					State string `json:"provisioningState"`
					Model struct {
						Name   string `json:"name"`
						Format string `json:"format"`
					} `json:"model"`
					Capabilities map[string]string `json:"capabilities"`
				} `json:"properties"`
			} `json:"value"`
			Next string `json:"nextLink"`
		}
		if err := c.request(ctx, http.MethodGet, next, scope, nil, &response); err != nil {
			return nil, err
		}
		if response.Value == nil {
			return nil, ErrUpstream
		}
		for _, d := range *response.Value {
			if !segment.MatchString(d.Name) || seen[d.Name] || !strings.EqualFold(d.ID, c.identity.ResourceID+"/deployments/"+d.Name) {
				return nil, ErrUpstream
			}
			seen[d.Name] = true
			if len(seen) > 1000 {
				return nil, ErrUpstream
			}
			model := strings.ToLower(d.Properties.Model.Name)
			chat := strings.EqualFold(d.Properties.Capabilities["chatCompletion"], "true") ||
				strings.HasPrefix(model, "gpt-") || model == "model-router"
			for key, value := range d.Properties.Capabilities {
				key = strings.ToLower(strings.NewReplacer("-", "", "_", "", " ", "").Replace(key))
				switch key {
				case "chatcompletion", "chatcompletions":
					if !strings.EqualFold(value, "true") {
						chat = false
					}
				case "batchonly", "responsesonly":
					if !strings.EqualFold(value, "false") {
						chat = false
					}
				}
			}
			if strings.Contains(strings.ToLower(d.SKU.Name), "batch") {
				chat = false
			}
			for _, excluded := range []string{"image", "audio", "realtime", "codex", "instruct", "-pro"} {
				if strings.Contains(model, excluded) {
					chat = false
				}
			}
			if chat && strings.EqualFold(d.Properties.Model.Format, "OpenAI") && strings.EqualFold(d.Properties.State, "Succeeded") {
				names = append(names, d.Name)
			}
		}
		next = response.Next
	}
	slices.Sort(names)
	c.mu.Lock()
	c.endpoint, c.deployments, c.refreshed = endpoint, names, time.Now()
	c.mu.Unlock()
	return slices.Clone(names), nil
}

// Extract returns validated suggestions, rejecting truncated model responses.
func (c *Azure) Extract(ctx context.Context, deployment, text string) ([]store.BankTransaction, error) {
	if text == "" || len(text) > MaxTextBytes {
		return nil, ErrResult
	}
	names, err := c.Deployments(ctx)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(names, deployment) {
		return nil, ErrDeployment
	}
	c.mu.RLock()
	endpoint := c.endpoint
	c.mu.RUnlock()
	if endpoint == "" {
		return nil, ErrUpstream
	}
	payload, err := json.Marshal(struct {
		Model     string              `json:"model"`
		Messages  []map[string]string `json:"messages"`
		MaxTokens int                 `json:"max_completion_tokens"`
	}{deployment, []map[string]string{
		{"role": "system", "content": extractionPrompt},
		{"role": "user", "content": "<statement>\n" + text + "\n</statement>"},
	}, 24000})
	if err != nil {
		return nil, ErrResult
	}
	var response struct {
		Choices []struct {
			Finish  string `json:"finish_reason"`
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	if err := c.request(ctx, http.MethodPost, endpoint+"/chat/completions",
		"https://cognitiveservices.azure.com/.default", payload, &response); err != nil {
		return nil, err
	}
	if len(response.Choices) != 1 || response.Choices[0].Finish != "stop" {
		return nil, ErrResult
	}
	return ParseResult(response.Choices[0].Message.Content)
}

var _ policy.Transporter = identityTransport{}
