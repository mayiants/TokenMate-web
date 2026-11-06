package azure

import (
	"testing"

	"github.com/ai-proxy/ai-proxy-go/pkg/relay/base"
)

func TestNewAzureAdaptor(t *testing.T) {
	cfg := &base.AdaptorConfig{
		BaseURL:    "https://my-resource.openai.azure.com/openai/deployments/my-deployment",
		APIKey:     "test-api-key",
		APIVersion: "2024-02-01",
	}
	adaptor := NewAzureAdaptor(cfg)

	if adaptor.GetProviderName() != "microsoft" {
		t.Errorf("Expected provider 'microsoft', got '%s'", adaptor.GetProviderName())
	}

	// Test headers use api-key, not Bearer
	headers := adaptor.GetHeaders()
	if headers["api-key"] != "test-api-key" {
		t.Errorf("Expected api-key header, got %v", headers)
	}
	if _, hasBearer := headers["Authorization"]; hasBearer {
		t.Error("Azure should not set Authorization header")
	}
}

func TestAzureGetRequestURL(t *testing.T) {
	// Case 1: BaseURL includes deployment
	cfg1 := &base.AdaptorConfig{
		BaseURL: "https://my-resource.openai.azure.com/openai/deployments/my-deployment",
		APIKey:  "test-key",
	}
	adaptor1 := NewAzureAdaptor(cfg1)
	url1 := adaptor1.GetRequestURL("gpt-4", false)
	expected1 := "https://my-resource.openai.azure.com/openai/deployments/my-deployment/chat/completions?api-version=2024-02-01"
	if url1 != expected1 {
		t.Errorf("URL mismatch:\n  got:      %s\n  expected: %s", url1, expected1)
	}

	// Case 2: BaseURL without deployment falls back to model name
	cfg2 := &base.AdaptorConfig{
		BaseURL: "https://my-resource.openai.azure.com",
		APIKey:  "test-key",
	}
	adaptor2 := NewAzureAdaptor(cfg2)
	url2 := adaptor2.GetRequestURL("gpt-4", false)
	expected2 := "https://my-resource.openai.azure.com/openai/deployments/gpt-4/chat/completions?api-version=2024-02-01"
	if url2 != expected2 {
		t.Errorf("URL mismatch:\n  got:      %s\n  expected: %s", url2, expected2)
	}
}

func TestAzureCustomAPIVersion(t *testing.T) {
	cfg := &base.AdaptorConfig{
		BaseURL:    "https://res.openai.azure.com/openai/deployments/dp",
		APIVersion: "2023-05-15",
	}
	adaptor := NewAzureAdaptor(cfg)
	endpoint := adaptor.GetEndpoint("", false)
	if endpoint != "/chat/completions?api-version=2023-05-15" {
		t.Errorf("Expected api-version 2023-05-15, got: %s", endpoint)
	}
}
