package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRelayFormat(t *testing.T) {
	t.Run("all relay format constants", func(t *testing.T) {
		assert.Equal(t, RelayFormat("openai"), RelayFormatOpenAI)
		assert.Equal(t, RelayFormat("claude"), RelayFormatClaude)
		assert.Equal(t, RelayFormat("gemini"), RelayFormatGemini)
		assert.Equal(t, RelayFormat("openai_realtime"), RelayFormatOpenAIRealtime)
		assert.Equal(t, RelayFormat("openai_responses"), RelayFormatOpenAIResponses)
		assert.Equal(t, RelayFormat("openai_responses_compaction"), RelayFormatOpenAIResponsesCompaction)
	})

	t.Run("relay format stringer", func(t *testing.T) {
		assert.Equal(t, "openai", string(RelayFormatOpenAI))
		assert.Equal(t, "claude", string(RelayFormatClaude))
		assert.Equal(t, "gemini", string(RelayFormatGemini))
	})

	t.Run("relay format comparison", func(t *testing.T) {
		assert.True(t, RelayFormatOpenAI == RelayFormat("openai"))
		assert.True(t, RelayFormatClaude != RelayFormatOpenAI)
		assert.True(t, RelayFormatGemini != RelayFormatClaude)
	})
}

func TestTokenType(t *testing.T) {
	t.Run("token type constants", func(t *testing.T) {
		assert.Equal(t, TokenType(1), TokenTypeTextNumber)
		assert.Equal(t, TokenType(2), TokenTypeTextToken)
	})
}

func TestFileType(t *testing.T) {
	t.Run("file type constants", func(t *testing.T) {
		assert.Equal(t, FileType("image"), FileTypeImage)
		assert.Equal(t, FileType("audio"), FileTypeAudio)
		assert.Equal(t, FileType("video"), FileTypeVideo)
		assert.Equal(t, FileType("file"), FileTypeFile)
	})
}

func TestFileMeta(t *testing.T) {
	t.Run("file meta struct", func(t *testing.T) {
		meta := FileMeta{
			FileType: FileTypeImage,
			Detail:   "high",
		}

		assert.Equal(t, FileTypeImage, meta.FileType)
		assert.Equal(t, "high", meta.Detail)
	})

	t.Run("file meta with audio", func(t *testing.T) {
		meta := FileMeta{
			FileType: FileTypeAudio,
			Detail:   "auto",
		}

		assert.Equal(t, FileTypeAudio, meta.FileType)
	})
}

func TestURLSource(t *testing.T) {
	t.Run("implements FileSource interface", func(t *testing.T) {
		var source FileSource = &URLSource{URL: "https://example.com/image.png"}
		assert.NotNil(t, source)
	})

	t.Run("IsURL returns true", func(t *testing.T) {
		source := &URLSource{URL: "https://example.com/image.png"}
		assert.True(t, source.IsURL())
	})

	t.Run("GetURL returns the URL", func(t *testing.T) {
		source := &URLSource{URL: "https://example.com/photo.jpg"}
		assert.Equal(t, "https://example.com/photo.jpg", source.GetURL())
	})

	t.Run("GetData returns nil", func(t *testing.T) {
		source := &URLSource{URL: "https://example.com/image.png"}
		assert.Nil(t, source.GetData())
	})

	t.Run("GetIdentifier returns URL", func(t *testing.T) {
		source := &URLSource{URL: "https://example.com/image.png"}
		assert.Equal(t, "https://example.com/image.png", source.GetIdentifier())
	})
}

func TestBase64Source(t *testing.T) {
	t.Run("implements FileSource interface", func(t *testing.T) {
		var source FileSource = &Base64Source{
			MediaType: "image/jpeg",
			Data:      "/9j/4AAQSkZJRg==",
		}
		assert.NotNil(t, source)
	})

	t.Run("IsURL returns false", func(t *testing.T) {
		source := &Base64Source{MediaType: "image/jpeg", Data: "base64data"}
		assert.False(t, source.IsURL())
	})

	t.Run("GetURL returns empty", func(t *testing.T) {
		source := &Base64Source{MediaType: "image/png", Data: "data"}
		assert.Empty(t, source.GetURL())
	})

	t.Run("GetData returns bytes", func(t *testing.T) {
		source := &Base64Source{MediaType: "image/jpeg", Data: "testdata"}
		assert.Equal(t, []byte("testdata"), source.GetData())
	})

	t.Run("GetIdentifier returns base64 prefix", func(t *testing.T) {
		source := &Base64Source{MediaType: "image/png", Data: "base64content"}
		assert.Equal(t, "base64:image/png", source.GetIdentifier())
	})

	t.Run("GetIdentifier with audio type", func(t *testing.T) {
		source := &Base64Source{MediaType: "audio/mp3", Data: "audio-data"}
		assert.Equal(t, "base64:audio/mp3", source.GetIdentifier())
	})
}

func TestTokenCountMeta(t *testing.T) {
	t.Run("empty meta", func(t *testing.T) {
		meta := TokenCountMeta{}
		assert.Equal(t, TokenType(0), meta.TokenType)
		assert.Equal(t, 0, meta.MessagesCount)
	})

	t.Run("full meta", func(t *testing.T) {
		meta := TokenCountMeta{
			TokenType:     TokenTypeTextToken,
			CombineText:   "Combine all text messages",
			MessagesCount: 5,
			ToolsCount:    2,
			NameCount:     1,
			Files: []*FileMeta{
				{FileType: FileTypeImage, Detail: "high"},
				{FileType: FileTypeAudio, Detail: "auto"},
			},
		}

		assert.Equal(t, TokenTypeTextToken, meta.TokenType)
		assert.Equal(t, "Combine all text messages", meta.CombineText)
		assert.Equal(t, 5, meta.MessagesCount)
		assert.Equal(t, 2, meta.ToolsCount)
		assert.Equal(t, 1, meta.NameCount)
		assert.Len(t, meta.Files, 2)
		assert.Equal(t, FileTypeImage, meta.Files[0].FileType)
		assert.Equal(t, FileTypeAudio, meta.Files[1].FileType)
	})
}

func TestPriceData(t *testing.T) {
	t.Run("default zero values", func(t *testing.T) {
		pd := PriceData{}
		assert.Equal(t, float64(0), pd.ModelRatio)
		assert.Equal(t, float64(0), pd.GroupRatio)
		assert.Equal(t, float64(0), pd.CompletionRatio)
		assert.Equal(t, float64(0), pd.CacheRatio)
		assert.Equal(t, float64(0), pd.CacheCreationRatio)
		assert.Equal(t, float64(0), pd.CacheCreation5mRatio)
		assert.Equal(t, float64(0), pd.CacheCreation1hRatio)
		assert.Equal(t, float64(0), pd.ImageRatio)
		assert.Equal(t, float64(0), pd.ModelPrice)
		assert.False(t, pd.UsePrice)
		assert.False(t, pd.FreeModel)
		assert.Equal(t, 0, pd.Quota)
	})

	t.Run("openai-like pricing", func(t *testing.T) {
		pd := PriceData{
			ModelRatio:      1.0,
			CompletionRatio: 3.0,
			CacheRatio:      0.1,
			Quota:           100000,
		}

		assert.Equal(t, float64(1.0), pd.ModelRatio)
		assert.Equal(t, float64(3.0), pd.CompletionRatio)
		assert.Equal(t, float64(0.1), pd.CacheRatio)
		assert.Equal(t, 100000, pd.Quota)
	})

	t.Run("claude-like pricing", func(t *testing.T) {
		pd := PriceData{
			ModelRatio:           15.0,
			CompletionRatio:      5.0,
			CacheCreation5mRatio: 0.25,
			CacheCreation1hRatio: 0.30,
		}

		assert.Equal(t, float64(15.0), pd.ModelRatio)
		assert.Equal(t, float64(5.0), pd.CompletionRatio)
		assert.Equal(t, float64(0.25), pd.CacheCreation5mRatio)
		assert.Equal(t, float64(0.30), pd.CacheCreation1hRatio)
	})

	t.Run("free model", func(t *testing.T) {
		pd := PriceData{
			FreeModel: true,
			Quota:     10,
		}

		assert.True(t, pd.FreeModel)
		assert.Equal(t, 10, pd.Quota)
	})

	t.Run("use price directly", func(t *testing.T) {
		pd := PriceData{
			UsePrice:   true,
			ModelPrice: 0.05,
			Quota:      5000,
		}

		assert.True(t, pd.UsePrice)
		assert.Equal(t, float64(0.05), pd.ModelPrice)
	})

	t.Run("with other ratios", func(t *testing.T) {
		pd := PriceData{
			ModelRatio:      1.0,
			CompletionRatio: 2.0,
			OtherRatios: map[string]float64{
				"special_model": 5.0,
				"premium":       10.0,
			},
		}

		assert.Len(t, pd.OtherRatios, 2)
		assert.Equal(t, float64(5.0), pd.OtherRatios["special_model"])
		assert.Equal(t, float64(10.0), pd.OtherRatios["premium"])
	})

	t.Run("full pricing config", func(t *testing.T) {
		pd := PriceData{
			ModelRatio:           2.0,
			GroupRatio:           1.5,
			CompletionRatio:      4.0,
			CacheRatio:           0.2,
			CacheCreationRatio:   0.5,
			CacheCreation5mRatio: 0.25,
			CacheCreation1hRatio: 0.30,
			ImageRatio:           2.0,
			ModelPrice:           0.03,
			UsePrice:             true,
			Quota:                1000000,
			OtherRatios:          map[string]float64{"audio": 1.5},
			FreeModel:            false,
		}

		assert.Equal(t, float64(2.0), pd.ModelRatio)
		assert.Equal(t, float64(1.5), pd.GroupRatio)
		assert.Equal(t, float64(4.0), pd.CompletionRatio)
		assert.Equal(t, float64(0.2), pd.CacheRatio)
		assert.Equal(t, float64(0.5), pd.CacheCreationRatio)
		assert.Equal(t, float64(0.25), pd.CacheCreation5mRatio)
		assert.Equal(t, float64(0.30), pd.CacheCreation1hRatio)
		assert.Equal(t, float64(2.0), pd.ImageRatio)
		assert.Equal(t, float64(0.03), pd.ModelPrice)
		assert.True(t, pd.UsePrice)
		assert.Equal(t, 1000000, pd.Quota)
		assert.Equal(t, float64(1.5), pd.OtherRatios["audio"])
		assert.False(t, pd.FreeModel)
	})
}

func TestFileSource_Interface(t *testing.T) {
	t.Run("URLSource satisfies FileSource", func(t *testing.T) {
		var _ FileSource = (*URLSource)(nil)
	})

	t.Run("Base64Source satisfies FileSource", func(t *testing.T) {
		var _ FileSource = (*Base64Source)(nil)
	})

	t.Run("nil URLSource", func(t *testing.T) {
		var source *URLSource
		assert.Nil(t, source)
	})

	t.Run("nil Base64Source", func(t *testing.T) {
		var source *Base64Source
		assert.Nil(t, source)
	})
}
