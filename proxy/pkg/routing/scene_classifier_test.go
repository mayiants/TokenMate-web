package routing

import (
	"testing"

	"github.com/ai-proxy/ai-proxy-go/dto"
	"github.com/stretchr/testify/assert"
)

func TestClassifyScene_Coding(t *testing.T) {
	tests := []struct {
		name     string
		messages []dto.Message
	}{
		{"explicit code request", msgs("Write a function to sort an array")},
		{"debug request", msgs("Debug this Python code: def foo():")},
		{"code block", msgs("Fix this:\n```go\nfunc main() {}\n```")},
		{"refactor", msgs("Refactor this class to use dependency injection")},
		{"Chinese coding", msgs("用golang实现一个算法")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, SceneCoding, ClassifyScene(tt.messages))
		})
	}
}

func TestClassifyScene_Search(t *testing.T) {
	tests := []struct {
		name     string
		messages []dto.Message
	}{
		{"what is", msgs("What is quantum computing?")},
		{"how to", msgs("How to make coffee?")},
		{"explain", msgs("Explain the theory of relativity")},
		{"translate", msgs("Translate this to French: hello world")},
		{"Chinese search", msgs("什么是机器学习？")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, SceneSearch, ClassifyScene(tt.messages))
		})
	}
}

func TestClassifyScene_Analysis(t *testing.T) {
	tests := []struct {
		name     string
		messages []dto.Message
	}{
		{"analyze", msgs("Analyze the market trends for Q4")},
		{"step by step", msgs("Think step by step about this problem")},
		{"evaluate", msgs("Evaluate the pros and cons of this approach")},
		{"mathematical", msgs("Calculate the derivative of x^2 + 3x")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, SceneAnalysis, ClassifyScene(tt.messages))
		})
	}
}

func TestClassifyScene_Creative(t *testing.T) {
	tests := []struct {
		name     string
		messages []dto.Message
	}{
		{"story", msgs("Write a story about a robot")},
		{"poem", msgs("Write a poem about autumn")},
		{"brainstorm", msgs("Brainstorm ideas for a startup")},
		{"Chinese creative", msgs("写一个故事关于太空探险")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, SceneCreative, ClassifyScene(tt.messages))
		})
	}
}

func TestClassifyScene_Image(t *testing.T) {
	tests := []struct {
		name     string
		messages []dto.Message
	}{
		{"draw", msgs("Draw a picture of a sunset")},
		{"generate image", msgs("Generate image of a cat")},
		{"Chinese image", msgs("画一个可爱的熊猫")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, SceneImage, ClassifyScene(tt.messages))
		})
	}
}

func TestClassifyScene_General(t *testing.T) {
	tests := []struct {
		name     string
		messages []dto.Message
	}{
		{"hi", msgs("Hello!")},
		{"thanks", msgs("Thank you for your help")},
		{"short", msgs("OK")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, SceneGeneral, ClassifyScene(tt.messages))
		})
	}
}

func TestClassifyScene_SystemPromptInfluence(t *testing.T) {
	messages := []dto.Message{
		{Role: "system", Content: "You are a Python coding assistant"},
		{Role: "user", Content: "Help me with this"},
	}
	// System prompt mentions coding → should classify as coding
	assert.Equal(t, SceneCoding, ClassifyScene(messages))
}

func TestResolveAutoModel(t *testing.T) {
	// Configure a mapping (simulates admin-configured scene routing)
	mapping := &SceneModelMapping{
		Coding:  []string{"claude-sonnet-4-6", "claude-opus-4-6"},
		Search:  []string{"gpt-5.4-mini", "gemini-2.5-flash"},
		General: []string{"gpt-5.4-mini"},
	}
	allAvailable := func(model string) bool { return true }

	// Coding scene → first coding model
	model, scene := ResolveAutoModel(msgs("Write a function"), mapping, allAvailable)
	assert.Equal(t, SceneCoding, scene)
	assert.Equal(t, "claude-sonnet-4-6", model)

	// Search scene → first search model
	model, scene = ResolveAutoModel(msgs("What is AI?"), mapping, allAvailable)
	assert.Equal(t, SceneSearch, scene)
	assert.Equal(t, "gpt-5.4-mini", model)
}

func TestResolveAutoModel_Fallback(t *testing.T) {
	mapping := &SceneModelMapping{
		Coding:  []string{"claude-sonnet-4-6", "claude-opus-4-6"},
		Search:  []string{"gpt-5.4-mini"},
		General: []string{"gpt-5.4-mini"},
	}

	// Only gpt-5.4-mini is available
	limited := func(model string) bool { return model == "gpt-5.4-mini" }

	model, scene := ResolveAutoModel(msgs("Write code"), mapping, limited)
	assert.Equal(t, SceneCoding, scene)
	// coding models not available, falls back to general → gpt-5.4-mini
	assert.Equal(t, "gpt-5.4-mini", model)
}

func TestResolveAutoModel_NilMapping(t *testing.T) {
	model, scene := ResolveAutoModel(msgs("Hello"), nil, nil)
	assert.Equal(t, SceneGeneral, scene)
	assert.Equal(t, "", model) // nil mapping = not configured = empty
}

func TestResolveAutoModel_EmptyMapping(t *testing.T) {
	mapping := DefaultSceneModelMapping()
	assert.False(t, mapping.IsConfigured())
	model, _ := ResolveAutoModel(msgs("Write code"), mapping, nil)
	assert.Equal(t, "", model) // empty mapping = not configured = empty
}

func TestResolveAutoModel_NoAvailableModel(t *testing.T) {
	mapping := &SceneModelMapping{
		Coding:  []string{"model-a", "model-b"},
		General: []string{"model-c"},
	}
	noneAvailable := func(model string) bool { return false }
	model, scene := ResolveAutoModel(msgs("Write code"), mapping, noneAvailable)
	assert.Equal(t, SceneCoding, scene)
	assert.Equal(t, "", model) // nothing available = empty (caller handles error)
}

// Helper to create simple user messages
func msgs(content string) []dto.Message {
	return []dto.Message{{Role: "user", Content: content}}
}
