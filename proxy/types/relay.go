package types

// RelayFormat - Request/response format type
type RelayFormat string

const (
	RelayFormatOpenAI                    RelayFormat = "openai"
	RelayFormatClaude                    RelayFormat = "claude"
	RelayFormatGemini                    RelayFormat = "gemini"
	RelayFormatOpenAIRealtime            RelayFormat = "openai_realtime"
	RelayFormatOpenAIResponses           RelayFormat = "openai_responses"
	RelayFormatOpenAIResponsesCompaction RelayFormat = "openai_responses_compaction"
)

// TokenType - Token counting type
type TokenType int

const (
	TokenTypeTextNumber TokenType = 1
	TokenTypeTextToken  TokenType = 2
)

// FileMeta - File metadata for token counting
type FileMeta struct {
	FileType FileType
	Source   FileSource
	Detail   string
}

// FileType - File type enumeration
type FileType string

const (
	FileTypeImage FileType = "image"
	FileTypeAudio FileType = "audio"
	FileTypeVideo FileType = "video"
	FileTypeFile  FileType = "file"
)

// FileSource - File source interface
type FileSource interface {
	IsURL() bool
	GetURL() string
	GetData() []byte
	GetIdentifier() string
}

// URLSource - URL-based file source
type URLSource struct {
	URL string
}

func (s *URLSource) IsURL() bool           { return true }
func (s *URLSource) GetURL() string        { return s.URL }
func (s *URLSource) GetData() []byte       { return nil }
func (s *URLSource) GetIdentifier() string { return s.URL }

// Base64Source - Base64-encoded file source
type Base64Source struct {
	MediaType string
	Data      string
}

func (s *Base64Source) IsURL() bool           { return false }
func (s *Base64Source) GetURL() string        { return "" }
func (s *Base64Source) GetData() []byte       { return []byte(s.Data) }
func (s *Base64Source) GetIdentifier() string { return "base64:" + s.MediaType }

// TokenCountMeta - Metadata for token counting
type TokenCountMeta struct {
	TokenType     TokenType
	CombineText   string
	MessagesCount int
	ToolsCount    int
	NameCount     int
	Files         []*FileMeta
}

// PriceData - Pricing data for billing.
// Absolute per-1M prices are preferred when populated by Portal sync.
// Ratio fields are kept for backward compatibility and are derived from
// absolute prices when syncing.
type PriceData struct {
	ModelRatio           float64
	GroupRatio           float64
	CompletionRatio      float64
	CacheRatio           float64
	CacheCreationRatio   float64
	CacheCreation5mRatio float64
	CacheCreation1hRatio float64
	ImageRatio           float64
	ModelPrice           float64
	UsePrice             bool
	Quota                int
	OtherRatios          map[string]float64
	FreeModel            bool

	// Absolute prices from Portal (USD per 1M tokens, except FixedPrice per request).
	InputPricePer1M      float64
	OutputPricePer1M     float64
	CacheHitPricePer1M   float64
	CacheWritePricePer1M float64
	ReasoningPricePer1M  float64
	FixedPrice           float64
	BillingMode          string
}
