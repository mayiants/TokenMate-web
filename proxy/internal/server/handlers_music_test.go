package server

import (
	"strings"
	"testing"
)

func TestAccumulateOpenRouterAudioSSE(t *testing.T) {
	sse := `: OPENROUTER PROCESSING

data: {"choices":[{"delta":{"content":"<instrumental>","role":"assistant"}}]}

data: {"choices":[{"delta":{"audio":{"data":"QUJD","format":"mp3"}}}]}

data: {"choices":[{"delta":{"audio":{"data":"REVG"}}}]}

data: [DONE]
`
	b64, format := accumulateOpenRouterAudioSSE(strings.NewReader(sse))
	if b64 != "QUJDREVG" {
		t.Errorf("b64 = %q, want QUJDREVG (concatenated)", b64)
	}
	if format != "mp3" {
		t.Errorf("format = %q, want mp3", format)
	}
}

func TestAccumulateOpenRouterAudioSSE_DataURI(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"audio\":{\"data\":\"data:audio/wav;base64,WAVE\",\"format\":\"wav\"}}}]}\n\ndata: [DONE]\n"
	b64, format := accumulateOpenRouterAudioSSE(strings.NewReader(sse))
	if b64 != "WAVE" || format != "wav" {
		t.Errorf("got b64=%q format=%q", b64, format)
	}
}

func TestMusicRequestText(t *testing.T) {
	if (musicRequest{Prompt: "p"}).text() != "p" {
		t.Error("prompt")
	}
	if (musicRequest{Input: "i"}).text() != "i" {
		t.Error("input fallback")
	}
}
