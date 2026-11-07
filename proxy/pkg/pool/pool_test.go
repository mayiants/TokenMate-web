package pool

import (
	"testing"

	"github.com/mayiants/ai-proxy-go/dto"
)

func TestPoolGetPutReturnsZeroedObject(t *testing.T) {
	req := GetGeneralOpenAIRequest()
	req.Model = "gpt-4o"
	req.Messages = []dto.Message{{Role: "user", Content: "hello"}}
	req.User = "alice"

	PutGeneralOpenAIRequest(req)

	req = GetGeneralOpenAIRequest()
	if req.Model != "" {
		t.Errorf("GeneralOpenAIRequest.Model not reset: got %q", req.Model)
	}
	if len(req.Messages) != 0 {
		t.Errorf("GeneralOpenAIRequest.Messages not reset: got %v", req.Messages)
	}
	if req.User != "" {
		t.Errorf("GeneralOpenAIRequest.User not reset: got %q", req.User)
	}
	PutGeneralOpenAIRequest(req)
}

func TestChatCompletionsStreamResponsePoolReset(t *testing.T) {
	resp := GetChatCompletionsStreamResponse()
	resp.ID = "chatcmpl-1"
	resp.Model = "gpt-4o"
	resp.Choices = []dto.ChatCompletionsStreamResponseChoice{{Index: 0}}
	usage := &dto.Usage{PromptTokens: 1}
	resp.Usage = usage

	PutChatCompletionsStreamResponse(resp)

	resp = GetChatCompletionsStreamResponse()
	if resp.ID != "" {
		t.Errorf("ChatCompletionsStreamResponse.ID not reset: got %q", resp.ID)
	}
	if resp.Model != "" {
		t.Errorf("ChatCompletionsStreamResponse.Model not reset: got %q", resp.Model)
	}
	if len(resp.Choices) != 0 {
		t.Errorf("ChatCompletionsStreamResponse.Choices not reset: got %v", resp.Choices)
	}
	if resp.Usage != nil {
		t.Errorf("ChatCompletionsStreamResponse.Usage not reset: got %v", resp.Usage)
	}
	PutChatCompletionsStreamResponse(resp)
}

func TestBytesBufferReset(t *testing.T) {
	buf := GetBuffer()
	buf.WriteString("some request body bytes")
	if buf.Len() == 0 {
		t.Fatal("buffer should contain data before put")
	}

	PutBuffer(buf)

	buf = GetBuffer()
	if buf.Len() != 0 {
		t.Errorf("buffer not reset on get: len=%d", buf.Len())
	}
	PutBuffer(buf)
}

func TestByteSliceSizeClasses(t *testing.T) {
	cases := []struct {
		minCap   int
		wantSize int
	}{
		{1, 4 * 1024},
		{4096, 4 * 1024},
		{4097, 16 * 1024},
		{16 * 1024, 16 * 1024},
		{100 * 1024, 256 * 1024},
		{1024 * 1024, 1024 * 1024},
		{2 * 1024 * 1024, 2 * 1024 * 1024}, // larger than largest class
	}

	for _, tc := range cases {
		b := GetBytes(tc.minCap)
		if cap(b) < tc.minCap {
			t.Errorf("GetBytes(%d) capacity %d < minCap %d", tc.minCap, cap(b), tc.minCap)
		}
		if tc.minCap <= byteSliceSizes[len(byteSliceSizes)-1] {
			if cap(b) != tc.wantSize {
				t.Errorf("GetBytes(%d) capacity %d, want %d", tc.minCap, cap(b), tc.wantSize)
			}
		}
		PutBytes(b)
	}
}

func TestByteSliceReusable(t *testing.T) {
	b := GetBytes(4096)
	b = b[:4096]
	copy(b, []byte("hello pool"))
	PutBytes(b)

	// sync.Pool does not guarantee FIFO/LIFO, so just verify GetBytes returns a
	// usable slice of the expected size class.
	b2 := GetBytes(4096)
	b2 = b2[:4096]
	if cap(b2) != 4096 {
		t.Errorf("GetBytes(4096) capacity %d, want 4096", cap(b2))
	}
	PutBytes(b2)
}

func TestPutNilDoesNotPanic(t *testing.T) {
	PutGeneralOpenAIRequest(nil)
	PutChatCompletionsStreamResponse(nil)
	PutBuffer(nil)
	PutBytes(nil)
}
