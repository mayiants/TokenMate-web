package aws

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSignRequest_SetsRequiredHeaders(t *testing.T) {
	body := []byte(`{"test":"payload"}`)
	req, err := http.NewRequest("POST", "https://bedrock.us-east-1.amazonaws.com/model/invoke", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	SignRequest(req, body, "AKID", "SECRET", "SESSION", "us-east-1", "bedrock")

	assert.NotEmpty(t, req.Header.Get("Authorization"))
	assert.NotEmpty(t, req.Header.Get("x-amz-date"))
	assert.NotEmpty(t, req.Header.Get("x-amz-content-sha256"))
	assert.Equal(t, "SESSION", req.Header.Get("x-amz-security-token"))
	assert.True(t, strings.HasPrefix(req.Header.Get("Authorization"), "AWS4-HMAC-SHA256"))
}

func TestSignRequest_NoSessionToken(t *testing.T) {
	req, err := http.NewRequest("GET", "https://bedrock.us-east-1.amazonaws.com/foundation-models", nil)
	require.NoError(t, err)

	SignRequest(req, []byte{}, "AKID", "SECRET", "", "us-east-1", "bedrock")

	assert.Empty(t, req.Header.Get("x-amz-security-token"))
}

func TestBuildCanonicalHeaders(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://bedrock.us-east-1.amazonaws.com/foundation-models", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-amz-date", "20260101T000000Z")
	req.Header.Set("x-amz-content-sha256", "payload-hash")

	canonical, signed := buildCanonicalHeaders(req)
	assert.Contains(t, canonical, "content-type:application/json")
	assert.Contains(t, canonical, "host:bedrock.us-east-1.amazonaws.com")
	assert.Contains(t, signed, "content-type")
	assert.Contains(t, signed, "host")
}

func TestGetSignatureKey_Deterministic(t *testing.T) {
	key1 := getSignatureKey("SECRET", "20260101", "us-east-1", "bedrock")
	key2 := getSignatureKey("SECRET", "20260101", "us-east-1", "bedrock")
	assert.Equal(t, key1, key2)

	key3 := getSignatureKey("OTHER", "20260101", "us-east-1", "bedrock")
	assert.NotEqual(t, key1, key3)
}

func TestSha256Hex(t *testing.T) {
	expected := sha256.Sum256([]byte("hello"))
	assert.Equal(t, hex.EncodeToString(expected[:]), sha256Hex([]byte("hello")))
}

func TestHmacHex(t *testing.T) {
	key := []byte("key")
	data := []byte("data")
	assert.NotEmpty(t, hmacHex(key, data))
}

func TestExtractRegionFromProvider(t *testing.T) {
	tests := []struct {
		provider string
		want     string
	}{
		{"amazon-us-east-1", "us-east-1"},
		{"amazon-mantle-us-west-2", "us-west-2"},
		{"amazon", "us-east-1"},
		{"openai", ""},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, ExtractRegionFromProvider(tt.provider), tt.provider)
	}
}

func TestParseAWSCredentials(t *testing.T) {
	access, secret, session, err := parseAWSCredentials("AKID:SECRET")
	require.NoError(t, err)
	assert.Equal(t, "AKID", access)
	assert.Equal(t, "SECRET", secret)
	assert.Empty(t, session)

	_, _, session, err = parseAWSCredentials("AKID:SECRET:SESSION")
	require.NoError(t, err)
	assert.Equal(t, "SESSION", session)

	_, _, _, err = parseAWSCredentials("only-one-part")
	assert.Error(t, err)

	_, _, _, err = parseAWSCredentials(":SECRET")
	assert.Error(t, err)
}

func TestIsBedrockRuntimeModel(t *testing.T) {
	tests := []struct {
		modelID string
		want    bool
	}{
		{"anthropic.claude-sonnet-4-6", true},
		{"amazon.nova-pro-v1:0", true},
		{"us.anthropic.claude-sonnet-4-6", true},
		{"eu.meta.llama3-70b-instruct-v1:0", true},
		{"openai.gpt-4o", false},
		{"deepseek.r1-v1:0", false},
		{"random-model", false},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, IsBedrockRuntimeModel(tt.modelID), tt.modelID)
	}
}

func TestFilterBedrockRuntimeModels(t *testing.T) {
	models := []BedrockModel{
		{ModelID: "anthropic.claude-sonnet-4-6"},
		{ModelID: "openai.gpt-4o"},
		{ModelID: "us.anthropic.claude-opus-4"},
	}
	filtered := FilterBedrockRuntimeModels(models)
	require.Len(t, filtered, 2)
	assert.Equal(t, "anthropic.claude-sonnet-4-6", filtered[0].ModelID)
	assert.Equal(t, "us.anthropic.claude-opus-4", filtered[1].ModelID)
}

func TestExtractProviderFromProfileID(t *testing.T) {
	tests := []struct {
		profileID string
		want      string
	}{
		{"us.anthropic.claude-sonnet-4-6", "anthropic"},
		{"anthropic.claude-opus-4", "anthropic"},
		{"us.", ""},
		{"model", ""},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, extractProviderFromProfileID(tt.profileID), tt.profileID)
	}
}
