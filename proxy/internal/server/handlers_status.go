package server

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// handleGetStatus returns overall system status
// GET /api/status
func (s *HandlerService) handleGetStatus(c *gin.Context) {
	if s.keyPoolManager == nil {
		c.JSON(http.StatusOK, gin.H{
			"status":     "operational",
			"lastUpdate": time.Now().Format(time.RFC3339),
			"providers":  []interface{}{},
		})
		return
	}

	stats := s.keyPoolManager.GetAllStats()
	var providers []gin.H
	allOperational := true

	for name, pool := range stats {
		status := "operational"
		if pool.ActiveKeys == 0 {
			status = "down"
			allOperational = false
		} else if pool.ActiveKeys < pool.TotalKeys {
			status = "degraded"
			allOperational = false
		}

		providers = append(providers, gin.H{
			"name":          name,
			"status":        status,
			"totalKeys":     pool.TotalKeys,
			"availableKeys": pool.ActiveKeys,
			"avgLatency":    0, // Would need actual metrics
		})
	}

	overallStatus := "operational"
	if !allOperational {
		overallStatus = "degraded"
	}

	c.JSON(http.StatusOK, gin.H{
		"status":     overallStatus,
		"lastUpdate": time.Now().Format(time.RFC3339),
		"providers":  providers,
	})
}

// GET /api/status/providers
func (s *HandlerService) handleGetStatusProviders(c *gin.Context) {
	if s.keyPoolManager == nil {
		c.JSON(http.StatusOK, gin.H{
			"providers": []interface{}{},
		})
		return
	}

	stats := s.keyPoolManager.GetAllStats()
	var providers []gin.H

	for name, pool := range stats {
		status := "operational"
		if pool.ActiveKeys == 0 {
			status = "down"
		} else if pool.ActiveKeys < pool.TotalKeys {
			status = "degraded"
		}

		providers = append(providers, gin.H{
			"name":          name,
			"displayName":   getDisplayName(name),
			"status":        status,
			"totalKeys":     pool.TotalKeys,
			"activeKeys":    pool.ActiveKeys,
			"avgLatency":    0,
			"totalRequests": pool.TotalRequests,
			"uptime":        99.9,
			"qps":           0,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"providers": providers,
	})
}

// GET /api/status/history
func (s *HandlerService) handleGetStatusHistory(c *gin.Context) {
	// limit parameter available for future use
	// if l := c.Query("limit"); l != "" { ... }

	// Return empty history for now (would need time-series storage)
	c.JSON(http.StatusOK, gin.H{
		"history": []interface{}{},
	})
}

// getDisplayName returns a friendly display name for a provider
func getDisplayName(name string) string {
	displayNames := map[string]string{
		"openai":    "OpenAI GPT",
		"anthropic": "Anthropic (Claude)",
		"google":    "Google Gemini",
		"deepseek":  "DeepSeek",
		"ali":       "Ali (阿里云)",
		"moonshot":  "Moonshot (Kimi)",
		"zhipu":     "Zhipu (智谱)",
		"tencent":   "Tencent (腾讯混元)",
		"bytedance": "Bytedance (字节Seed)",
		"minimax":   "MiniMax",
		"mistral":   "Mistral",
		"cohere":    "Cohere",
		"xai":       "xAI (Grok)",
		"meta":      "Meta Llama",
		"amazon":    "Amazon Bedrock",
		"microsoft": "Microsoft Azure",
	}
	if dn, ok := displayNames[name]; ok {
		return dn
	}
	return name
}

// handleGetProviderStats returns stats for all providers
// GET /providers/stats
func (s *HandlerService) handleGetProviderStats(c *gin.Context) {
	if s.keyPoolManager == nil {
		c.JSON(http.StatusOK, gin.H{})
		return
	}

	stats := s.keyPoolManager.GetAllStats()
	c.JSON(http.StatusOK, stats)
}

// handleHeartbeat handles proxy heartbeat
// POST /api/webhook/heartbeat
func (s *HandlerService) handleHeartbeat(c *gin.Context) {
	var req struct {
		ServerID      string                 `json:"serverId"`
		Uptime        int64                  `json:"uptime"`
		TotalRequests int64                  `json:"totalRequests"`
		Providers     map[string]interface{} `json:"providers"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request body"}})
		return
	}

	// Process heartbeat (would store in database or memory)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// GET /models/stats
func (s *HandlerService) handleGetModelStats(c *gin.Context) {
	if s.modelRegistry == nil {
		c.JSON(http.StatusOK, gin.H{
			"status": "not_initialized",
		})
		return
	}

	c.JSON(http.StatusOK, s.modelRegistry.GetStats())
}

// GET /models/list
func (s *HandlerService) handleGetModelList(c *gin.Context) {
	if s.modelRegistry == nil {
		c.JSON(http.StatusOK, gin.H{
			"models": []interface{}{},
		})
		return
	}

	provider := c.Query("provider")
	if provider != "" {
		// Return models for specific provider
		modelIDs := s.modelRegistry.GetProviderModels(provider)
		c.JSON(http.StatusOK, gin.H{
			"provider": provider,
			"models":   modelIDs,
		})
		return
	}

	// Return all providers
	c.JSON(http.StatusOK, gin.H{
		"providers": s.modelRegistry.GetAllProviders(),
	})
}
