package routes

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/mayiants/ai-portal-go/internal/services"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// pricingRuleRequest is the payload for creating/updating a pricing rule.
type pricingRuleRequest struct {
	Provider      string                   `json:"provider" binding:"required"`
	Region        string                   `json:"region"`
	ProductID     string                   `json:"product_id"`
	APIModelName  string                   `json:"api_model_name"`
	BillingMode   string                   `json:"billing_mode" binding:"required"`
	Prices        services.ComponentPrices `json:"prices"`
	Tiers         []services.TokenTier     `json:"tiers"`
	TimeRules     []services.TimeRule      `json:"time_rules"`
	Timezone      string                   `json:"timezone"`
	Currency      string                   `json:"currency"`
	EffectiveFrom *time.Time               `json:"effective_from"`
	EffectiveTo   *time.Time               `json:"effective_to"`
	Priority      int                      `json:"priority"`
	Status        string                   `json:"status"`
	RuleName      string                   `json:"rule_name"`
	Note          string                   `json:"note"`
	BlockSeconds  int                      `json:"block_seconds"`
}

func (r *AdminRoutes) listPricingRules(c *gin.Context) {
	provider := c.Query("provider")
	region := c.Query("region")
	model := c.Query("model")
	billingMode := c.Query("billing_mode")
	status := c.Query("status")

	rules, err := r.pricingSvc.ListRules(c.Request.Context(), provider, region, model, billingMode, status)
	if err != nil {
		r.logger.Error("failed to list pricing rules", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list pricing rules"}})
		return
	}

	out := make([]pricingRuleJSON, 0, len(rules))
	for _, rule := range rules {
		out = append(out, ruleToJSON(rule))
	}
	c.JSON(http.StatusOK, gin.H{"rules": out})
}

func (r *AdminRoutes) getPricingRule(c *gin.Context) {
	id := c.Param("id")
	rule, err := r.pricingSvc.GetRule(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Pricing rule not found"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"rule": ruleToJSON(rule)})
}

func (r *AdminRoutes) createPricingRule(c *gin.Context) {
	var req pricingRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	rule := r.reqToRule(&req)
	id, err := r.pricingSvc.CreateRule(c.Request.Context(), rule)
	if err != nil {
		r.logger.Error("failed to create pricing rule", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to create pricing rule"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "id": id})
}

func (r *AdminRoutes) updatePricingRule(c *gin.Context) {
	id := c.Param("id")
	var req pricingRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	rule := r.reqToRule(&req)
	rule.ID = id
	if err := r.pricingSvc.UpdateRule(c.Request.Context(), rule); err != nil {
		r.logger.Error("failed to update pricing rule", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update pricing rule"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (r *AdminRoutes) deletePricingRule(c *gin.Context) {
	id := c.Param("id")
	if err := r.pricingSvc.DeleteRule(c.Request.Context(), id); err != nil {
		r.logger.Error("failed to delete pricing rule", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to delete pricing rule"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (r *AdminRoutes) togglePricingRuleStatus(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()

	rule, err := r.pricingSvc.GetRule(ctx, id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Pricing rule not found"}})
		return
	}

	if rule.Status == "active" {
		rule.Status = "inactive"
	} else {
		rule.Status = "active"
	}
	if err := r.pricingSvc.UpdateRule(ctx, rule); err != nil {
		r.logger.Error("failed to toggle pricing rule status", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to toggle pricing rule status"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "status": rule.Status})
}

func (r *AdminRoutes) exportPricingCSV(c *gin.Context) {
	provider := c.Query("provider")
	region := c.Query("region")
	model := c.Query("model")
	billingMode := c.Query("billing_mode")
	status := c.Query("status")
	if status == "" {
		status = "active"
	}

	data, err := r.pricingSvc.ExportRulesToCSV(c.Request.Context(), provider, region, model, billingMode, status)
	if err != nil {
		r.logger.Error("failed to export pricing rules", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to export pricing rules"}})
		return
	}

	filename := fmt.Sprintf("pricing-rules-%s.csv", time.Now().UTC().Format("200601021504"))
	c.Header("Content-Disposition", "attachment; filename=\""+filename+"\"")
	c.Data(http.StatusOK, "text/csv; charset=utf-8", data)
}

func (r *AdminRoutes) importPricingCSV(c *gin.Context) {
	file, _, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Missing CSV file"}})
		return
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(file)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Failed to read CSV file"}})
		return
	}

	created, updated, failed, failures, err := r.pricingSvc.ImportRulesFromCSV(c.Request.Context(), data)
	if err != nil {
		r.logger.Error("failed to import pricing rules", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"created":  created,
		"updated":  updated,
		"failed":   failed,
		"failures": failures,
		"message":  fmt.Sprintf("Created %d, updated %d, failed %d", created, updated, failed),
	})
}

func (r *AdminRoutes) reqToRule(req *pricingRuleRequest) *services.PricingRule {
	effectiveFrom := time.Now()
	if req.EffectiveFrom != nil {
		effectiveFrom = *req.EffectiveFrom
	}
	status := req.Status
	if status == "" {
		status = "active"
	}
	tz := req.Timezone
	if tz == "" {
		tz = "UTC"
	}
	currency := req.Currency
	if currency == "" {
		currency = "USD"
	}
	region := req.Region
	if region == "" {
		region = "*"
	}

	return &services.PricingRule{
		Provider:      req.Provider,
		Region:        region,
		ProductID:     req.ProductID,
		APIModelName:  req.APIModelName,
		BillingMode:   req.BillingMode,
		Prices:        req.Prices,
		Tiers:         req.Tiers,
		TimeRules:     req.TimeRules,
		Timezone:      tz,
		Currency:      currency,
		EffectiveFrom: effectiveFrom,
		EffectiveTo:   req.EffectiveTo,
		Priority:      req.Priority,
		Status:        status,
		RuleName:      req.RuleName,
		Note:          req.Note,
		BlockSeconds:  req.BlockSeconds,
	}
}

// pricingRuleJSON is used to render a pricing rule with explicit JSON fields.
type pricingRuleJSON struct {
	ID            string                   `json:"id"`
	Provider      string                   `json:"provider"`
	Region        string                   `json:"region"`
	ProductID     string                   `json:"product_id"`
	APIModelName  string                   `json:"api_model_name"`
	BillingMode   string                   `json:"billing_mode"`
	Prices        services.ComponentPrices `json:"prices"`
	Tiers         []services.TokenTier     `json:"tiers"`
	TimeRules     []services.TimeRule      `json:"time_rules"`
	Timezone      string                   `json:"timezone"`
	Currency      string                   `json:"currency"`
	EffectiveFrom time.Time                `json:"effective_from"`
	EffectiveTo   *time.Time               `json:"effective_to"`
	Priority      int                      `json:"priority"`
	Status        string                   `json:"status"`
	RuleName      string                   `json:"rule_name"`
	Note          string                   `json:"note"`
	BlockSeconds  int                      `json:"block_seconds"`
	CreatedAt     time.Time                `json:"created_at"`
	UpdatedAt     time.Time                `json:"updated_at"`
}

func ruleToJSON(r *services.PricingRule) pricingRuleJSON {
	return pricingRuleJSON{
		ID:            r.ID,
		Provider:      r.Provider,
		Region:        r.Region,
		ProductID:     r.ProductID,
		APIModelName:  r.APIModelName,
		BillingMode:   r.BillingMode,
		Prices:        r.Prices,
		Tiers:         r.Tiers,
		TimeRules:     r.TimeRules,
		Timezone:      r.Timezone,
		Currency:      r.Currency,
		EffectiveFrom: r.EffectiveFrom,
		EffectiveTo:   r.EffectiveTo,
		Priority:      r.Priority,
		Status:        r.Status,
		RuleName:      r.RuleName,
		Note:          r.Note,
		BlockSeconds:  r.BlockSeconds,
		CreatedAt:     r.CreatedAt,
		UpdatedAt:     r.UpdatedAt,
	}
}
