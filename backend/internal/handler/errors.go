package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/repository"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/service"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/util"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func handleError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		util.Fail(c, http.StatusNotFound, "not_found", "record was not found")
	case errors.Is(err, repository.ErrVersionConflict), isLockContention(err):
		// A lost state-machine race (optimistic version) or a database-level
		// lock/serialization conflict both mean this request must be retried
		// after a refresh; they never corrupt the aggregate.
		util.Fail(c, http.StatusConflict, "version_conflict", "record changed or locked by another request; refresh and retry")
	case errors.Is(err, service.ErrQualificationInvalid):
		util.Fail(c, http.StatusUnprocessableEntity, "qualification_invalid", err.Error())
	case errors.Is(err, service.ErrSnapshotInvalid), errors.Is(err, service.ErrSnapshotNotFrozen):
		util.Fail(c, http.StatusUnprocessableEntity, "snapshot_rule", err.Error())
	case errors.Is(err, service.ErrInvalidTransition), errors.Is(err, service.ErrInvalidInput):
		util.Fail(c, http.StatusUnprocessableEntity, "business_rule", err.Error())
	default:
		_ = c.Error(err)
		util.Fail(c, http.StatusInternalServerError, "internal_error", "request could not be completed")
	}
}

// isLockContention recognizes driver-level concurrency errors that should be
// surfaced as a retryable conflict instead of a 500: SQLite busy/locked and
// PostgreSQL deadlock/serialization failures.
func isLockContention(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "database is locked") ||
		strings.Contains(message, "sqlite_busy") ||
		strings.Contains(message, "deadlock detected") ||
		strings.Contains(message, "could not serialize access") ||
		strings.Contains(message, "try restarting transaction")
}
