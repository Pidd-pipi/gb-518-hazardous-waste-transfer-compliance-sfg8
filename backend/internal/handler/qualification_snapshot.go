package handler

import (
	"net/http"
	"strings"

	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/service"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/util"
	"github.com/gin-gonic/gin"
)

// QualificationSnapshotHandler exposes the append-only 资质快照 history that the
// 核验页 reads to make decisions. It never returns live permit/license data.
type QualificationSnapshotHandler struct {
	service service.QualificationSnapshotService
}

func NewQualificationSnapshotHandler(s service.QualificationSnapshotService) *QualificationSnapshotHandler {
	return &QualificationSnapshotHandler{service: s}
}

func (h *QualificationSnapshotHandler) Register(group *gin.RouterGroup) {
	group.GET("/snapshots/:manifestCode", h.list)
}

func (h *QualificationSnapshotHandler) list(c *gin.Context) {
	manifestCode := strings.ToUpper(strings.TrimSpace(c.Param("manifestCode")))
	if manifestCode == "" {
		util.Fail(c, http.StatusBadRequest, "invalid_request", "manifestCode is required")
		return
	}
	history, err := h.service.ListByManifestCode(c.Request.Context(), manifestCode)
	if err != nil {
		handleError(c, err)
		return
	}
	var latest any
	if len(history) > 0 {
		latest = history[0]
	}
	util.OK(c, gin.H{"manifestCode": manifestCode, "latest": latest, "history": history})
}
