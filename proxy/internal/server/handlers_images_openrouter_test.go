package server

import (
	"encoding/json"
	"testing"
)

func TestImagesRequestToOpenRouterChat(t *testing.T) {
	in := []byte(`{"model":"google/gemini-2.5-flash-image","prompt":"a red circle","n":1}`)
	out, err := imagesRequestToOpenRouterChat(in)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	var chat struct {
		Model      string                   `json:"model"`
		Modalities []string                 `json:"modalities"`
		Messages   []map[string]interface{} `json:"messages"`
	}
	if err := json.Unmarshal(out, &chat); err != nil {
		t.Fatalf("unmarshal chat: %v", err)
	}
	if chat.Model != "google/gemini-2.5-flash-image" {
		t.Errorf("model = %q", chat.Model)
	}
	if len(chat.Modalities) != 2 || chat.Modalities[0] != "image" {
		t.Errorf("modalities = %v, want [image text]", chat.Modalities)
	}
	if len(chat.Messages) != 1 || chat.Messages[0]["content"] != "a red circle" {
		t.Errorf("messages = %v", chat.Messages)
	}

	if _, err := imagesRequestToOpenRouterChat([]byte(`{"model":"x","prompt":""}`)); err == nil {
		t.Errorf("empty prompt should error")
	}
}

func TestOpenRouterChatToImagesResponse(t *testing.T) {
	t.Run("data-uri image -> b64_json", func(t *testing.T) {
		chat := []byte(`{"created":123,"choices":[{"message":{"images":[{"image_url":{"url":"data:image/png;base64,AAAABBBB"}}]}}],"usage":{"total_tokens":42}}`)
		out, ok := openRouterChatToImagesResponse(chat)
		if !ok {
			t.Fatal("expected ok")
		}
		var r struct {
			Created int64 `json:"created"`
			Data    []struct {
				B64JSON string `json:"b64_json"`
			} `json:"data"`
			Usage map[string]interface{} `json:"usage"`
		}
		if err := json.Unmarshal(out, &r); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(r.Data) != 1 || r.Data[0].B64JSON != "AAAABBBB" {
			t.Errorf("data = %+v, want b64_json=AAAABBBB", r.Data)
		}
		if r.Created != 123 {
			t.Errorf("created = %d", r.Created)
		}
		if r.Usage == nil {
			t.Errorf("usage should pass through")
		}
	})

	t.Run("hosted url -> url field", func(t *testing.T) {
		chat := []byte(`{"choices":[{"message":{"images":[{"image_url":{"url":"https://cdn.example/img.png"}}]}}]}`)
		out, ok := openRouterChatToImagesResponse(chat)
		if !ok {
			t.Fatal("expected ok")
		}
		var r struct {
			Data []struct {
				URL     string `json:"url"`
				B64JSON string `json:"b64_json"`
			} `json:"data"`
		}
		_ = json.Unmarshal(out, &r)
		if len(r.Data) != 1 || r.Data[0].URL != "https://cdn.example/img.png" || r.Data[0].B64JSON != "" {
			t.Errorf("data = %+v, want url only", r.Data)
		}
	})

	t.Run("no image part -> not ok (passthrough)", func(t *testing.T) {
		chat := []byte(`{"choices":[{"message":{"content":"sorry, text only"}}]}`)
		if _, ok := openRouterChatToImagesResponse(chat); ok {
			t.Errorf("expected not ok for text-only response")
		}
	})
}

func TestIsOpenRouterBaseURL(t *testing.T) {
	if !isOpenRouterBaseURL("https://openrouter.ai/api/v1") {
		t.Error("should detect openrouter")
	}
	if isOpenRouterBaseURL("https://api.laozhang.ai/v1") {
		t.Error("should not match laozhang")
	}
}
