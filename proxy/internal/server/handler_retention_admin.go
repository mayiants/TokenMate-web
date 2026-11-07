package server

import (
	"net/http"
	"strconv"

	"github.com/mayiants/ai-proxy-go/internal/retention"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// retentionAccess records every admin read/list/purge of retained content, both
// in the structured log and in the append-only local access log. Retained
// payloads are customer data, so who touched what must be reconstructible.
func (s *HandlerService) retentionAccess(c *gin.Context, action, traceID, enterpriseID, result string) {
	ip := c.ClientIP()
	if s.logger != nil {
		s.logger.Info("retention_admin_access",
			zap.String("action", action),
			zap.String("trace_id", traceID),
			zap.String("enterprise_id", enterpriseID),
			zap.String("client_ip", ip),
			zap.String("result", result),
		)
	}
	if ts, ok := s.retentionStore.(retention.TenantStore); ok {
		if err := ts.AppendAccessLog(retention.AccessEntry{
			Action:       action,
			TraceID:      traceID,
			EnterpriseID: enterpriseID,
			ClientIP:     ip,
			Result:       result,
		}); err != nil && s.logger != nil {
			s.logger.Warn("Failed to append retention access log", zap.Error(err))
		}
	}
}

// handleGetRetentionObject retrieves a locally-stored retention record by trace ID.
// This is the minimal admin API counterpart to the S3 object-admin API described
// in the data-retention design doc. With ?enterprise_id=<id> only that tenant's
// subtree is searched (fast path); without it the whole tree is walked.
func (s *HandlerService) handleGetRetentionObject(c *gin.Context) {
	traceID := c.Param("trace_id")
	entID := c.Query("enterprise_id")
	if traceID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "trace_id is required"}})
		return
	}

	if s.retentionStore == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "retention store is not configured"}})
		return
	}

	var (
		rec *retention.Record
		err error
	)
	if ts, ok := s.retentionStore.(retention.TenantStore); ok && entID != "" {
		rec, err = ts.GetForEnterprise(entID, traceID)
	} else {
		rec, err = s.retentionStore.Get(traceID)
	}
	if err != nil || rec == nil {
		if s.logger != nil {
			s.logger.Warn("Retention object lookup failed",
				zap.String("trace_id", traceID),
				zap.Error(err),
			)
		}
		s.retentionAccess(c, "read", traceID, entID, "not_found")
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "retention object not found"}})
		return
	}

	s.retentionAccess(c, "read", traceID, entID, "ok")
	c.JSON(http.StatusOK, rec)
}

// tenantStore returns the per-enterprise store or writes a 501 response.
func (s *HandlerService) tenantStore(c *gin.Context) (retention.TenantStore, bool) {
	ts, ok := s.retentionStore.(retention.TenantStore)
	if !ok {
		c.JSON(http.StatusNotImplemented, gin.H{"error": gin.H{"message": "retention store does not support tenant operations"}})
		return nil, false
	}
	return ts, true
}

// handleListRetentionEnterpriseObjects lists a tenant's records, newest first.
func (s *HandlerService) handleListRetentionEnterpriseObjects(c *gin.Context) {
	entID := c.Param("ent_id")
	ts, ok := s.tenantStore(c)
	if !ok {
		return
	}
	limit := 0
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			s.retentionAccess(c, "list", "", entID, "bad_request")
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "invalid limit"}})
			return
		}
		limit = n
	}
	objs, err := ts.ListObjects(entID, limit, c.Query("date"))
	if err != nil {
		s.retentionAccess(c, "list", "", entID, "bad_request")
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}
	s.retentionAccess(c, "list", "", entID, "ok")
	c.JSON(http.StatusOK, gin.H{"enterprise_id": entID, "count": len(objs), "objects": objs})
}

// handlePurgeRetentionEnterprise deletes everything retained for a tenant. The
// caller must repeat the enterprise id in ?confirm= so a mistyped URL or a
// replayed request cannot wipe a different tenant.
func (s *HandlerService) handlePurgeRetentionEnterprise(c *gin.Context) {
	entID := c.Param("ent_id")
	ts, ok := s.tenantStore(c)
	if !ok {
		return
	}
	if c.Query("confirm") != entID {
		s.retentionAccess(c, "purge", "", entID, "confirm_mismatch")
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "confirm query parameter must equal ent_id"}})
		return
	}
	res, err := ts.PurgeEnterprise(entID)
	if err != nil {
		s.retentionAccess(c, "purge", "", entID, "error")
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}
	s.retentionAccess(c, "purge", "", entID, "ok")
	c.JSON(http.StatusOK, gin.H{"deleted_files": res.DeletedFiles, "deleted_bytes": res.DeletedBytes})
}
