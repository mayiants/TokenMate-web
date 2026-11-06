package retention

// Record is the on-disk representation of a single request/response pair.
// It mirrors the object format described in the data-retention design doc,
// but is stored locally as gzip-compressed JSON instead of in S3.
type Record struct {
	SchemaVersion string      `json:"schema_version"`
	TraceID       string      `json:"trace_id"`
	ReceivedAt    string      `json:"received_at"`
	Metadata      Metadata    `json:"metadata"`
	Request       PayloadPart `json:"request"`
	Response      PayloadPart `json:"response"`
}

// Metadata holds the queryable attributes for a retained request.
type Metadata struct {
	UserID            string `json:"user_id,omitempty"`
	APIKeyHash        string `json:"api_key_hash,omitempty"`
	ClientIP          string `json:"client_ip,omitempty"`
	UpstreamKeyID     string `json:"upstream_key_id,omitempty"`
	Provider          string `json:"provider,omitempty"`
	Model             string `json:"model,omitempty"`
	VendorModelName   string `json:"vendor_model_name,omitempty"`
	ClientFormat      string `json:"client_format,omitempty"`
	UpstreamFormat    string `json:"upstream_format,omitempty"`
	Region            string `json:"region,omitempty"`
	StatusCode        int    `json:"status_code,omitempty"`
	LatencyMs         int64  `json:"latency_ms,omitempty"`
	FirstTokenLatency int64  `json:"first_token_latency_ms,omitempty"`
	InputTokens       int    `json:"input_tokens,omitempty"`
	OutputTokens      int    `json:"output_tokens,omitempty"`
	CachedTokens      int    `json:"cached_tokens,omitempty"`
	IsStream          bool   `json:"is_stream,omitempty"`
	IsConverted       bool   `json:"is_converted,omitempty"`
	RoutingTrack      string `json:"routing_track,omitempty"`
	IsFallback        bool   `json:"is_fallback,omitempty"`
	RequestType       string `json:"request_type,omitempty"`

	// Per-enterprise retention policy applied when the record was written.
	EnterpriseID  string `json:"enterprise_id,omitempty"`
	RetentionMode string `json:"retention_mode,omitempty"`
	RetentionDays int    `json:"retention_days,omitempty"`
	// ResponseCaptured is false when no response body was stored (streaming
	// responses, or metadata-only mode); never omitted so absence is explicit.
	ResponseCaptured bool `json:"response_captured"`
	// Truncated is true when the stored payload is intentionally incomplete
	// (streamed response not buffered, or bodies blanked by metadata mode).
	Truncated bool `json:"truncated,omitempty"`
}

// PayloadPart captures one side of the HTTP exchange.
type PayloadPart struct {
	Method  string            `json:"method,omitempty"`
	Path    string            `json:"path,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    []byte            `json:"body,omitempty"`
}
