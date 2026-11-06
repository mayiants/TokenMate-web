package server

import (
	"github.com/ai-proxy/ai-proxy-go/pkg/relay"
	"github.com/ai-proxy/ai-proxy-go/pkg/sse"
	"github.com/ai-proxy/ai-proxy-go/types"
)

// sseFormatForAdaptor maps a relay adaptor's output format to the SSE parser dialect.
// It is used to detect stream completion/error markers correctly for the upstream.
func sseFormatForAdaptor(adaptor relay.Adaptor) sse.Format {
	switch adaptor.GetRelayFormat() {
	case types.RelayFormatClaude:
		return sse.FormatAnthropic
	case types.RelayFormatOpenAIResponses, types.RelayFormatOpenAIResponsesCompaction:
		return sse.FormatResponses
	case types.RelayFormatOpenAI, types.RelayFormatGemini, types.RelayFormatOpenAIRealtime:
		return sse.FormatOpenAI
	default:
		return sse.FormatOpenAI
	}
}
