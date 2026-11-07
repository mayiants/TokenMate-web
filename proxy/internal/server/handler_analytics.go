package server

import (
	"errors"
	"net/http"

	"github.com/mayiants/ai-proxy-go/internal/retention/analytics"
	"github.com/gin-gonic/gin"
)

// handleGetAnalyticsSummary returns the full latest analytics report.
func (s *HandlerService) handleGetAnalyticsSummary(c *gin.Context) {
	sched := s.getAnalyticsScheduler()
	if sched == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{"message": "analytics not enabled (retention is disabled)"},
		})
		return
	}

	report := sched.GetReport()
	if report == nil {
		c.JSON(http.StatusAccepted, gin.H{
			"message": "analytics report is being generated, please retry shortly",
		})
		return
	}

	c.JSON(http.StatusOK, report)
}

// handleGetAnalyticsModels returns model rankings and capabilities.
func (s *HandlerService) handleGetAnalyticsModels(c *gin.Context) {
	sched := s.getAnalyticsScheduler()
	if sched == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{"message": "analytics not enabled"},
		})
		return
	}

	report := sched.GetReport()
	if report == nil {
		c.JSON(http.StatusAccepted, gin.H{"message": "report pending"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"generated_at": report.GeneratedAt,
		"models":       report.Models,
	})
}

// handleGetAnalyticsPerformance returns performance metrics and trends.
func (s *HandlerService) handleGetAnalyticsPerformance(c *gin.Context) {
	sched := s.getAnalyticsScheduler()
	if sched == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{"message": "analytics not enabled"},
		})
		return
	}

	report := sched.GetReport()
	if report == nil {
		c.JSON(http.StatusAccepted, gin.H{"message": "report pending"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"generated_at": report.GeneratedAt,
		"performance":  report.Performance,
		"trends":       report.Trends,
	})
}

// handleGetAnalyticsReliability returns reliability and error analysis.
func (s *HandlerService) handleGetAnalyticsReliability(c *gin.Context) {
	sched := s.getAnalyticsScheduler()
	if sched == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{"message": "analytics not enabled"},
		})
		return
	}

	report := sched.GetReport()
	if report == nil {
		c.JSON(http.StatusAccepted, gin.H{"message": "report pending"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"generated_at": report.GeneratedAt,
		"reliability":  report.Reliability,
	})
}

// handleGetAnalyticsRefresh triggers an immediate re-analysis.
func (s *HandlerService) handleGetAnalyticsRefresh(c *gin.Context) {
	sched := s.getAnalyticsScheduler()
	if sched == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{"message": "analytics not enabled"},
		})
		return
	}

	report, err := sched.RunNow()
	if err != nil {
		if errors.Is(err, analytics.ErrAnalysisRunning) {
			c.JSON(http.StatusConflict, gin.H{
				"error": gin.H{"message": "analysis already in progress"},
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"message": "analysis failed: " + err.Error()},
		})
		return
	}

	c.JSON(http.StatusOK, report)
}

// getAnalyticsScheduler retrieves the scheduler from the handler service.
func (s *HandlerService) getAnalyticsScheduler() *analytics.Scheduler {
	return s.analyticsScheduler
}
