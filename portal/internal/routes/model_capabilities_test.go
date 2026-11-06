package routes

import "testing"

func TestNormalizeCapabilityTypes(t *testing.T) {
	cases := []struct {
		in  []string
		out []string
	}{
		{[]string{"Text", "IMAGE", "text"}, []string{"text", "image"}},
		{[]string{"audio", "text"}, []string{"text", "audio"}},
		{[]string{"banana", "video"}, []string{"video"}},
		{nil, nil},
		{[]string{" embedding "}, []string{"embedding"}},
	}
	for _, c := range cases {
		got := normalizeCapabilityTypes(c.in)
		if len(got) != len(c.out) {
			t.Errorf("normalizeCapabilityTypes(%v) = %v, want %v", c.in, got, c.out)
			continue
		}
		for i := range got {
			if got[i] != c.out[i] {
				t.Errorf("normalizeCapabilityTypes(%v) = %v, want %v", c.in, got, c.out)
				break
			}
		}
	}
}

func TestInferCapabilityTypes(t *testing.T) {
	cases := []struct {
		name string
		out  []string
	}{
		{"gpt-image-2", []string{"text", "image"}},
		{"seedream-5.0-pro", []string{"text", "image"}},
		{"kling-v3", []string{"text", "video"}},
		{"sora-2-pro", []string{"text", "video"}},
		{"gpt-4o-audio-preview", []string{"text", "audio"}},
		{"text-embedding-3-large", []string{"embedding"}},
		{"claude-opus-4-7", []string{"text"}},
		{"deepseek-v4-flash", []string{"text"}},
	}
	for _, c := range cases {
		got := inferCapabilityTypes(c.name)
		if len(got) != len(c.out) {
			t.Errorf("inferCapabilityTypes(%q) = %v, want %v", c.name, got, c.out)
			continue
		}
		for i := range got {
			if got[i] != c.out[i] {
				t.Errorf("inferCapabilityTypes(%q) = %v, want %v", c.name, got, c.out)
				break
			}
		}
	}
}
