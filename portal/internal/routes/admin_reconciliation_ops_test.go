package routes

import (
	"testing"
)

// TestAdminRoutesReconciliationOpsMethodsExist is a compile-time check.
func TestAdminRoutesReconciliationOpsMethodsExist(t *testing.T) {
	_ = (*AdminRoutes).parseReconciliationRange
	_ = (*AdminRoutes).getUpstreamReconciliationSummary
	_ = (*AdminRoutes).getUpstreamReconciliationTrend
	_ = (*AdminRoutes).getUpstreamReconciliationDetail
	_ = (*AdminRoutes).exportUpstreamReconciliation
}
