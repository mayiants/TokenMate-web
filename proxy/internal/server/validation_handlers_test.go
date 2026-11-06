package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestHandleValidateModel_NoValidator(t *testing.T) {
	// Ensure validator is nil
	service := NewHandlerService(nil, nil)
	service.modelValidator = nil

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := `{"provider": "openai", "product_id": "gpt-4o"}`
	c.Request = httptest.NewRequest("POST", "/api/internal/validate-model", bytes.NewReader([]byte(body)))

	service.handleValidateModel(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	errObj, ok := resp["error"].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, "Model validator not initialized", errObj["message"])
}

func TestHandleValidateModel_InvalidBody(t *testing.T) {
	service := NewHandlerService(nil, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/internal/validate-model", bytes.NewReader([]byte("not json")))

	service.handleValidateModel(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleValidateModel_MissingFields(t *testing.T) {
	service := NewHandlerService(nil, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	// Missing required "product_id" field
	body := `{"provider": "openai"}`
	c.Request = httptest.NewRequest("POST", "/api/internal/validate-model", bytes.NewReader([]byte(body)))

	service.handleValidateModel(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleBatchValidate_NoValidator(t *testing.T) {
	service := NewHandlerService(nil, nil)
	service.modelValidator = nil

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := `{"models": [{"provider": "openai", "product_id": "gpt-4o"}]}`
	c.Request = httptest.NewRequest("POST", "/api/internal/batch-validate", bytes.NewReader([]byte(body)))

	service.handleBatchValidate(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	errObj, ok := resp["error"].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, "Model validator not initialized", errObj["message"])
}

func TestHandleBatchValidate_InvalidBody(t *testing.T) {
	service := NewHandlerService(nil, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/internal/batch-validate", bytes.NewReader([]byte("not json")))

	service.handleBatchValidate(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleBatchValidate_EmptyModels(t *testing.T) {
	service := NewHandlerService(nil, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	// Empty models array — should still require models field
	body := `{}`
	c.Request = httptest.NewRequest("POST", "/api/internal/batch-validate", bytes.NewReader([]byte(body)))

	service.handleBatchValidate(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleBatchValidate_MissingFieldsInModel(t *testing.T) {
	service := NewHandlerService(nil, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	// Each model entry requires "provider" and "product_id" but ShouldBindJSON
	// only validates the top-level "models" required tag, not nested fields.
	// Since JSON binds successfully and modelValidator is nil, returns 500.
	body := `{"models": [{"provider": "openai"}]}`
	c.Request = httptest.NewRequest("POST", "/api/internal/batch-validate", bytes.NewReader([]byte(body)))

	service.handleBatchValidate(c)

	// Returns 500 because modelValidator is nil (not a bind error for nested fields)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
