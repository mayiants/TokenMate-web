package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client talks to the portal's agent endpoints (/api/v1/agent). Server is the
// portal base URL the agent enrolled against (e.g. https://123.60.182.168);
// APIBaseURL returned by exchange is written into client configs and may
// differ (public gateway hostname).
type Client struct {
	Server   string
	Insecure bool
	HTTP     *http.Client
}

func NewClient(server string, insecure bool) *Client {
	tr := &http.Transport{}
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec -- explicit test-mode flag until a real CA cert is deployed
	}
	return &Client{
		Server:   server,
		Insecure: insecure,
		HTTP:     &http.Client{Transport: tr, Timeout: 60 * time.Second},
	}
}

type apiError struct {
	Message string `json:"message"`
}

func (e *apiError) Error() string { return e.Message }

func (c *Client) post(ctx context.Context, path string, body, out interface{}) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Server+"/api/v1/agent"+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, out)
}

func (c *Client) get(ctx context.Context, path string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Server+"/api/v1/agent"+path, nil)
	if err != nil {
		return err
	}
	return c.do(req, out)
}

func (c *Client) do(req *http.Request, out interface{}) error {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		var wrapper struct {
			Error apiError `json:"error"`
		}
		if json.Unmarshal(raw, &wrapper) == nil && wrapper.Error.Message != "" {
			return &wrapper.Error
		}
		return fmt.Errorf("portal returned %d: %s", resp.StatusCode, string(raw))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

type OTPSendResponse struct {
	Success bool `json:"success"`
}

// SendOTP emails a 6-digit code to the member. Always succeeds from the
// caller's perspective unless the portal rate-limits (429).
func (c *Client) SendOTP(ctx context.Context, email string) error {
	var out OTPSendResponse
	err := c.post(ctx, "/otp/send", map[string]string{"email": email}, &out)
	if err != nil {
		return err
	}
	if !out.Success {
		return fmt.Errorf("portal rejected OTP request")
	}
	return nil
}

type VerifyResponse struct {
	Success      bool   `json:"success"`
	SessionToken string `json:"session_token"`
	ExpiresIn    int    `json:"expires_in"`
	MemberName   string `json:"member_name"`
}

func (c *Client) VerifyOTP(ctx context.Context, email, code string) (*VerifyResponse, error) {
	var out VerifyResponse
	if err := c.post(ctx, "/otp/verify", map[string]string{"email": email, "code": code}, &out); err != nil {
		return nil, err
	}
	if out.SessionToken == "" {
		return nil, fmt.Errorf("portal returned no session token")
	}
	return &out, nil
}

type AdapterManifest struct {
	Client         string          `json:"client"`
	OS             string          `json:"os"`
	ConfigPath     string          `json:"config_path"`
	InjectMethod   string          `json:"inject_method"`
	EnvBaseURLVar  string          `json:"env_base_url_var"`
	EnvAPIKeyVar   string          `json:"env_api_key_var"`
	Manifest       json.RawMessage `json:"manifest"`
	Version        int             `json:"version"`
	Status         string          `json:"status,omitempty"` // "no_adapter" when none is registered
}

type ExchangeResponse struct {
	Success    bool             `json:"success"`
	APIKey     string           `json:"api_key"`
	KeyID      string           `json:"key_id"`
	DeviceID   string           `json:"device_id"`
	APIBaseURL string           `json:"api_base_url"`
	Adapter    *AdapterManifest `json:"adapter"`
}

func (c *Client) Exchange(ctx context.Context, req map[string]string) (*ExchangeResponse, error) {
	var out ExchangeResponse
	if err := c.post(ctx, "/enroll/exchange", req, &out); err != nil {
		return nil, err
	}
	if out.APIKey == "" {
		return nil, fmt.Errorf("portal returned no api key")
	}
	return &out, nil
}

type HeartbeatResponse struct {
	Success       bool   `json:"success"`
	KeyStatus     string `json:"key_status"`
	ConfigVersion int    `json:"config_version"`
}

func (c *Client) Heartbeat(ctx context.Context, bearer string, body map[string]string) (*HeartbeatResponse, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Server+"/api/v1/agent/heartbeat", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	var out HeartbeatResponse
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetConfig(ctx context.Context, clientName, osName string) (*AdapterManifest, error) {
	var out AdapterManifest
	if err := c.get(ctx, "/config?client="+clientName+"&os="+osName, &out); err != nil {
		return nil, err
	}
	if out.Status == "no_adapter" {
		return nil, fmt.Errorf("no adapter registered for %s/%s", clientName, osName)
	}
	return &out, nil
}
