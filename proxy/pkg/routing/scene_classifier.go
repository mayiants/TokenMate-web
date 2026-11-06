// Package routing provides intelligent request routing based on content analysis
// and cost optimization.
package routing

import (
	"strings"

	"github.com/ai-proxy/ai-proxy-go/dto"
)

// SceneType represents the classified intent of a user request.
type SceneType string

const (
	SceneCoding   SceneType = "coding"
	SceneSearch   SceneType = "search"
	SceneCreative SceneType = "creative"
	SceneAnalysis SceneType = "analysis"
	SceneImage    SceneType = "image"
	SceneGeneral  SceneType = "general"
)

// ClassifyScene analyzes the system prompt and user messages to determine the
// request's primary intent. This is used for model=auto routing to select the
// most appropriate model for the task.
func ClassifyScene(messages []dto.Message) SceneType {
	var systemText, userText string

	for _, m := range messages {
		switch m.Role {
		case "system":
			systemText += " " + extractText(m)
		case "user":
			userText += " " + extractText(m)
		}
	}

	combined := strings.ToLower(systemText + " " + userText)

	// Check for image generation (highest specificity)
	if containsAny(combined, imageKeywords) {
		return SceneImage
	}

	// Check for coding (high specificity — code blocks, technical terms)
	if containsAny(combined, codingKeywords) || strings.Contains(combined, "```") {
		return SceneCoding
	}

	// Check for analysis/reasoning
	if containsAny(combined, analysisKeywords) {
		return SceneAnalysis
	}

	// Check for creative writing
	if containsAny(combined, creativeKeywords) {
		return SceneCreative
	}

	// Check for search/QA
	if containsAny(combined, searchKeywords) {
		return SceneSearch
	}

	return SceneGeneral
}

// --- Keyword sets ---

var codingKeywords = []string{
	"code", "function", "implement", "debug", "refactor",
	"programming", "algorithm", "class ", "api ", "bug",
	"compile", "syntax", "variable", "import ", "package",
	"git ", "deploy", "docker", "kubernetes", "terraform",
	"python", "javascript", "golang", "rust ", "java ",
	"typescript", "sql ", "html", "css ", "react",
	"test case", "unit test", "fix the", "write a function",
	"代码", "编程", "函数", "调试", "重构", "算法", "实现一个",
}

var searchKeywords = []string{
	"what is", "what are", "how to", "explain",
	"define", "summary", "summarize", "translate",
	"find ", "search", "look up", "tell me about",
	"difference between", "compare ",
	"什么是", "如何", "解释", "翻译", "总结", "查找",
}

var analysisKeywords = []string{
	"analyze", "analyse", "evaluate", "assess",
	"review ", "compare and contrast", "pros and cons",
	"think step by step", "reasoning", "logical",
	"calculate", "derive", "prove", "mathematical",
	"data analysis", "statistics", "regression",
	"分析", "评估", "推理", "逐步思考", "计算",
}

var creativeKeywords = []string{
	"write a story", "write a poem", "creative",
	"fiction", "narrative", "novel", "essay",
	"brainstorm", "generate ideas", "imagine",
	"screenplay", "dialogue", "lyrics",
	"写一个故事", "创作", "小说", "诗歌", "散文",
}

var imageKeywords = []string{
	"draw", "generate image", "generate a picture",
	"create an image", "illustration", "logo design",
	"画", "生成图片", "图像",
}

// containsAny checks if text contains any of the keywords.
func containsAny(text string, keywords []string) bool {
	for _, kw := range keywords {
		if strings.Contains(text, kw) {
			return true
		}
	}
	return false
}

// extractText gets the text content from a message (handles string content).
func extractText(m dto.Message) string {
	if m.Content == nil {
		return ""
	}
	// Content can be a string or array of content parts
	if s, ok := m.Content.(string); ok {
		return s
	}
	// For multimodal content, try to extract text parts
	if parts, ok := m.Content.([]interface{}); ok {
		var texts []string
		for _, part := range parts {
			if mp, ok := part.(map[string]interface{}); ok {
				if t, ok := mp["text"].(string); ok {
					texts = append(texts, t)
				}
			}
		}
		return strings.Join(texts, " ")
	}
	return ""
}

// SceneModelMapping defines which models to use for each scene type.
// The first entry is preferred; subsequent entries are fallbacks.
// This should be configured by admin (via Portal group settings), not hardcoded.
type SceneModelMapping struct {
	Coding   []string `json:"coding" yaml:"coding"`
	Search   []string `json:"search" yaml:"search"`
	Creative []string `json:"creative" yaml:"creative"`
	Analysis []string `json:"analysis" yaml:"analysis"`
	Image    []string `json:"image" yaml:"image"`
	General  []string `json:"general" yaml:"general"`
}

// DefaultSceneModelMapping returns a minimal fallback mapping.
// In production this should be overridden by Portal admin configuration.
// The default uses generic model families rather than specific versions.
func DefaultSceneModelMapping() *SceneModelMapping {
	return &SceneModelMapping{
		Coding:   []string{}, // empty = use whatever model the user/group is configured for
		Search:   []string{},
		Creative: []string{},
		Analysis: []string{},
		Image:    []string{},
		General:  []string{},
	}
}

// IsConfigured returns true if the mapping has at least one scene with models.
func (m *SceneModelMapping) IsConfigured() bool {
	if m == nil {
		return false
	}
	return len(m.Coding) > 0 || len(m.Search) > 0 || len(m.Creative) > 0 ||
		len(m.Analysis) > 0 || len(m.Image) > 0 || len(m.General) > 0
}

// GetModelsForScene returns the ordered list of recommended models for a scene.
func (m *SceneModelMapping) GetModelsForScene(scene SceneType) []string {
	switch scene {
	case SceneCoding:
		return m.Coding
	case SceneSearch:
		return m.Search
	case SceneCreative:
		return m.Creative
	case SceneAnalysis:
		return m.Analysis
	case SceneImage:
		return m.Image
	case SceneGeneral:
		return m.General
	default:
		return m.General
	}
}

// ResolveAutoModel classifies the scene from messages and returns the best
// available model. It checks each recommended model against the availability
// function and returns the first one that is available.
// Returns empty string if no model is available (caller should handle this error).
func ResolveAutoModel(messages []dto.Message, mapping *SceneModelMapping, isAvailable func(model string) bool) (model string, scene SceneType) {
	if mapping == nil || !mapping.IsConfigured() {
		return "", SceneGeneral
	}

	scene = ClassifyScene(messages)
	candidates := mapping.GetModelsForScene(scene)

	for _, candidate := range candidates {
		if isAvailable == nil || isAvailable(candidate) {
			return candidate, scene
		}
	}

	// Try general fallback
	for _, candidate := range mapping.General {
		if isAvailable == nil || isAvailable(candidate) {
			return candidate, scene
		}
	}

	// Nothing available
	return "", scene
}
