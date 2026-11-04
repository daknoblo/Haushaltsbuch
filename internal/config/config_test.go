package config

import (
	"strings"
	"testing"
)

func TestAIConfigurationIsOptionalAndExplicit(t *testing.T) {
	for _, key := range []string{"AZURE_RESOURCE_ID", "AZURE_TENANT_ID", "AZURE_CLIENT_ID", "AZURE_CLIENT_SECRET"} {
		t.Setenv(key, "")
	}
	t.Setenv("HB_DATA_DIR", t.TempDir())
	t.Setenv("HB_HTTP_ADDR", ":8080")
	if cfg := Load(); cfg.AI.Enabled() || cfg.Validate() != nil {
		t.Fatal("AI must remain optional")
	}
	t.Setenv("AZURE_CLIENT_SECRET", "synthetic-secret")
	if err := Load().Validate(); err == nil || strings.Contains(err.Error(), "synthetic-secret") {
		t.Fatal("partial identity must fail without disclosing its secret")
	}
}
