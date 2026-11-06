package routes

import (
	"testing"
)

// TestAdminRoutesModelMethodsExist is a compile-time check that verifies
// all admin model handler methods compile. All require a live database.
func TestAdminRoutesModelMethodsExist(t *testing.T) {
	// Verified: listServiceModels, updateServiceModel, batchValidateModels,
	// listModelsGrouped, listModelsAdmin, getModelsStatus, getSyncStatus,
	// syncOpenRouter, syncOpenRouterLive, validateOpenRouter,
	// getOpenRouterModels, syncFromAIModelsStub, syncBedrockModels,
	// syncMantleModels, syncFromUpstreamKey
	_ = (*AdminRoutes).listServiceModels
	_ = (*AdminRoutes).updateServiceModel
	_ = (*AdminRoutes).batchValidateModels
	_ = (*AdminRoutes).listModelsGrouped
	_ = (*AdminRoutes).listModelsAdmin
	_ = (*AdminRoutes).getModelsStatus
	_ = (*AdminRoutes).getSyncStatus
	_ = (*AdminRoutes).syncOpenRouter
	_ = (*AdminRoutes).syncOpenRouterLive
	_ = (*AdminRoutes).validateOpenRouter
	_ = (*AdminRoutes).getOpenRouterModels
	_ = (*AdminRoutes).syncFromAIModelsStub
	_ = (*AdminRoutes).syncBedrockModels
	_ = (*AdminRoutes).syncMantleModels
	_ = (*AdminRoutes).syncFromUpstreamKey
}
