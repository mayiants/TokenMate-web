package convert

import (
	"github.com/mayiants/ai-proxy-go/dto"
)

// GeminiContent represents a single content block in Gemini format.
type GeminiContent struct {
	Role  string       `json:"role"`
	Parts []GeminiPart `json:"parts"`
}

// GeminiPart represents a part of Gemini content.
type GeminiPart struct {
	Text       string            `json:"text,omitempty"`
	InlineData *GeminiInlineData `json:"inlineData,omitempty"`
}

// GeminiInlineData represents inline binary data in Gemini format.
type GeminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

// GeminiRequest is a simplified Gemini request structure.
type GeminiRequest struct {
	Contents          []GeminiContent        `json:"contents"`
	SystemInstruction *GeminiContent         `json:"systemInstruction,omitempty"`
	GenerationConfig  map[string]interface{} `json:"generationConfig,omitempty"`
	Tools             []interface{}          `json:"tools,omitempty"`
}

// GeminiResponse is a simplified Gemini response structure.
type GeminiResponse struct {
	Candidates    []GeminiCandidate `json:"candidates"`
	UsageMetadata GeminiUsage       `json:"usageMetadata,omitempty"`
}

// GeminiCandidate represents a response candidate.
type GeminiCandidate struct {
	Content      GeminiContent `json:"content"`
	FinishReason string        `json:"finishReason,omitempty"`
}

// GeminiUsage represents token usage in Gemini format.
type GeminiUsage struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

// RequestOpenAI2Gemini converts an OpenAI request to Gemini format.
func RequestOpenAI2Gemini(req dto.GeneralOpenAIRequest) (*GeminiRequest, error) {
	geminiReq := &GeminiRequest{
		GenerationConfig: make(map[string]interface{}),
	}

	// Set generation parameters
	if req.Temperature != nil {
		geminiReq.GenerationConfig["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		geminiReq.GenerationConfig["topP"] = *req.TopP
	}
	if req.TopK != nil {
		geminiReq.GenerationConfig["topK"] = *req.TopK
	}
	if req.MaxTokens != nil {
		geminiReq.GenerationConfig["maxOutputTokens"] = *req.MaxTokens
	}
	if req.Stop != nil {
		switch v := req.Stop.(type) {
		case string:
			if v != "" {
				geminiReq.GenerationConfig["stopSequences"] = []string{v}
			}
		case []string:
			geminiReq.GenerationConfig["stopSequences"] = v
		}
	}

	// Separate system messages
	var systemParts []GeminiPart
	contents := make([]GeminiContent, 0, len(req.Messages))

	for _, msg := range req.Messages {
		if msg.Role == "system" {
			text := ExtractStringContent(msg.Content)
			if text != "" {
				systemParts = append(systemParts, GeminiPart{Text: text})
			}
			continue
		}

		role := ToGeminiRole(msg.Role)
		parts := messageContentToGeminiParts(msg.Content)

		contents = append(contents, GeminiContent{
			Role:  role,
			Parts: parts,
		})
	}

	if len(systemParts) > 0 {
		geminiReq.SystemInstruction = &GeminiContent{
			Role:  "user",
			Parts: systemParts,
		}
	}

	geminiReq.Contents = contents

	return geminiReq, nil
}

// messageContentToGeminiParts converts OpenAI message content to Gemini parts.
func messageContentToGeminiParts(content interface{}) []GeminiPart {
	if content == nil {
		return nil
	}

	if s, ok := content.(string); ok {
		return []GeminiPart{{Text: s}}
	}

	if mediaList, ok := content.([]dto.MediaContent); ok {
		parts := make([]GeminiPart, 0, len(mediaList))
		for _, mc := range mediaList {
			switch mc.Type {
			case "text":
				parts = append(parts, GeminiPart{Text: mc.Text})
			case "image_url":
				if mc.ImageURL != nil {
					parts = append(parts, GeminiPart{
						InlineData: &GeminiInlineData{
							MimeType: "image/jpeg",
							Data:     mc.ImageURL.URL,
						},
					})
				}
			}
		}
		return parts
	}

	// Fallback: extract as string
	text := ExtractStringContent(content)
	if text != "" {
		return []GeminiPart{{Text: text}}
	}
	return nil
}

// ResponseGemini2OpenAI converts a Gemini response to OpenAI format.
func ResponseGemini2OpenAI(geminiResp *GeminiResponse, model string) *dto.OpenAITextResponse {
	if geminiResp == nil || len(geminiResp.Candidates) == 0 {
		return nil
	}

	candidate := geminiResp.Candidates[0]

	// Extract text from parts
	var content string
	for _, part := range candidate.Content.Parts {
		content += part.Text
	}

	usage := UsageFromGemini(geminiResp.UsageMetadata)

	finishReason := StopReasonToOpenAI(candidate.FinishReason)

	return &dto.OpenAITextResponse{
		ID:     "gemini-response",
		Object: "chat.completion",
		Model:  model,
		Choices: []dto.OpenAITextResponseChoice{
			{
				Index: 0,
				Message: dto.Message{
					Role:    "assistant",
					Content: content,
				},
				FinishReason: finishReason,
			},
		},
		Usage: usage,
	}
}

// UsageFromGemini converts Gemini usage to OpenAI-compatible Usage.
func UsageFromGemini(geminiUsage GeminiUsage) dto.Usage {
	return dto.Usage{
		PromptTokens:     geminiUsage.PromptTokenCount,
		CompletionTokens: geminiUsage.CandidatesTokenCount,
		TotalTokens:      geminiUsage.TotalTokenCount,
		InputTokens:      geminiUsage.PromptTokenCount,
		OutputTokens:     geminiUsage.CandidatesTokenCount,
	}
}

// StreamResponseGemini2OpenAI converts a Gemini stream chunk to OpenAI stream format.
// Note: This is a simplified version. Real Gemini streaming uses Server-Sent Events
// with different structure than OpenAI.
func StreamResponseGemini2OpenAI(geminiResp *GeminiResponse) *dto.ChatCompletionsStreamResponse {
	if geminiResp == nil || len(geminiResp.Candidates) == 0 {
		return nil
	}

	candidate := geminiResp.Candidates[0]
	var text string
	for _, part := range candidate.Content.Parts {
		text += part.Text
	}

	if text == "" && candidate.FinishReason == "" {
		return nil
	}

	var finishReasonPtr *string
	if candidate.FinishReason != "" {
		fr := StopReasonToOpenAI(candidate.FinishReason)
		finishReasonPtr = &fr
	}

	contentPtr := &text
	if text == "" {
		contentPtr = nil
	}

	return &dto.ChatCompletionsStreamResponse{
		ID:     "gemini-stream",
		Object: "chat.completion.chunk",
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{
				Index: 0,
				Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
					Content: contentPtr,
				},
				FinishReason: finishReasonPtr,
			},
		},
	}
}

// FormatErrorMessageGemini formats a Gemini error into OpenAI-compatible error format.
func FormatErrorMessageGemini(message string) map[string]interface{} {
	return FormatErrorMessage(message, "invalid_request_error", "gemini_error")
}
