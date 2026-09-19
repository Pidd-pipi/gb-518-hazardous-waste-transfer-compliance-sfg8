package handler

import (
	"net/http"
	"strings"

	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/dto"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/middleware"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/model"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/service"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/util"
	"github.com/gin-gonic/gin"
)

type TransferManifestHandler struct {
	service service.TransferManifestService
}

func NewTransferManifestHandler(s service.TransferManifestService) *TransferManifestHandler {
	return &TransferManifestHandler{service: s}
}

func (h *TransferManifestHandler) Register(group *gin.RouterGroup) {
	resource := group.Group("/manifests")
	resource.GET("", h.list)
	resource.GET("/snapshots", h.snapshots)
	resource.GET("/:id", h.get)
	resource.POST("", middleware.RequireMinimumRole(model.RoleOperator), h.create)
	resource.PUT("/:id", middleware.RequireMinimumRole(model.RoleOperator), h.update)
	resource.POST("/:id/transition", middleware.RequireMinimumRole(model.RoleOperator), h.transition)
	resource.DELETE("/:id", middleware.RequireRoles(model.RoleAdmin), h.remove)
}

func (h *TransferManifestHandler) list(c *gin.Context) {
	query := bindPage(c)
	result, err := h.service.List(c.Request.Context(), query)
	if err != nil {
		handleError(c, err)
		return
	}
	util.Page(c, result.Items, result.Page, result.PageSize, result.Total)
}

func (h *TransferManifestHandler) get(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	item, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		handleError(c, err)
		return
	}
	util.OK(c, item)
}

// snapshots serves the frozen qualification snapshots the verification page
// needs: snapshot version, validity window and invalid reason per manifest.
func (h *TransferManifestHandler) snapshots(c *gin.Context) {
	raw := strings.TrimSpace(c.Query("codes"))
	if raw == "" {
		util.Fail(c, http.StatusBadRequest, "invalid_request", "codes query parameter is required")
		return
	}
	codes := strings.Split(raw, ",")
	if len(codes) > 100 {
		util.Fail(c, http.StatusBadRequest, "invalid_request", "at most 100 manifest codes per snapshot query")
		return
	}
	result, err := h.service.Snapshots(c.Request.Context(), codes)
	if err != nil {
		handleError(c, err)
		return
	}
	util.OK(c, result)
}

func (h *TransferManifestHandler) create(c *gin.Context) {
	var input dto.CreateTransferManifest
	if err := c.ShouldBindJSON(&input); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	item, err := h.service.Create(c.Request.Context(), input, actorFromContext(c), requestIDFromContext(c))
	if err != nil {
		handleError(c, err)
		return
	}
	util.Created(c, item)
}

func (h *TransferManifestHandler) update(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var input dto.UpdateTransferManifest
	if err := c.ShouldBindJSON(&input); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	item, err := h.service.Update(c.Request.Context(), id, input, actorFromContext(c), requestIDFromContext(c))
	if err != nil {
		handleError(c, err)
		return
	}
	util.OK(c, item)
}

func (h *TransferManifestHandler) transition(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var input dto.TransitionRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	item, err := h.service.Transition(c.Request.Context(), id, input, actorFromContext(c), requestIDFromContext(c))
	if err != nil {
		handleError(c, err)
		return
	}
	util.OK(c, item)
}

func (h *TransferManifestHandler) remove(c *gin.Context) {
	if roleFromContext(c) != "admin" {
		util.Fail(c, http.StatusForbidden, "forbidden", "admin role is required")
		return
	}
	id, ok := parseID(c)
	if !ok {
		return
	}
	if err := h.service.Delete(c.Request.Context(), id, actorFromContext(c), requestIDFromContext(c)); err != nil {
		handleError(c, err)
		return
	}
	util.NoContent(c)
}
