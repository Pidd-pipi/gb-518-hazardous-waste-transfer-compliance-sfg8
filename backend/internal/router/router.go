package router

import (
	"log/slog"
	"net/http"

	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/config"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/handler"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/middleware"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/model"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/repository"
	"github.com/blueship581/hazardous-waste-transfer-compliance/backend/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

func New(cfg config.Config, db *gorm.DB, redisClient *redis.Client, logger *slog.Logger) *gin.Engine {
	if cfg.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}
	engine := gin.New()
	engine.Use(middleware.RequestContext(logger))
	if len(cfg.TrustedProxies) > 0 {
		_ = engine.SetTrustedProxies(cfg.TrustedProxies)
	} else {
		_ = engine.SetTrustedProxies(nil)
	}

	securityRepository := repository.NewSecurityRepository(db)
	securityService := service.NewSecurityService(securityRepository, cfg)
	txManager := repository.NewTxManager(db)
	wasteGeneratorRepository := repository.NewWasteGeneratorRepository(db)
	carrierProfileRepository := repository.NewCarrierProfileRepository(db)
	transferManifestRepository := repository.NewTransferManifestRepository(db)
	qualificationSnapshotRepository := repository.NewQualificationSnapshotRepository(db)
	complianceCheckRepository := repository.NewComplianceCheckRepository(db)
	wasteGeneratorService := service.NewWasteGeneratorService(wasteGeneratorRepository, securityService)
	carrierProfileService := service.NewCarrierProfileService(carrierProfileRepository, securityService)
	qualificationSnapshotService := service.NewQualificationSnapshotService(qualificationSnapshotRepository, wasteGeneratorRepository, carrierProfileRepository)
	transferManifestService := service.NewTransferManifestService(transferManifestRepository, wasteGeneratorRepository, carrierProfileRepository, qualificationSnapshotService, txManager)
	complianceCheckService := service.NewComplianceCheckService(complianceCheckRepository, transferManifestRepository, qualificationSnapshotService)
	wasteGeneratorHandler := handler.NewWasteGeneratorHandler(wasteGeneratorService)
	carrierProfileHandler := handler.NewCarrierProfileHandler(carrierProfileService)
	transferManifestHandler := handler.NewTransferManifestHandler(transferManifestService)
	complianceCheckHandler := handler.NewComplianceCheckHandler(complianceCheckService)
	qualificationSnapshotHandler := handler.NewQualificationSnapshotHandler(qualificationSnapshotService)
	systemHandler := handler.NewSystemHandler(securityService, wasteGeneratorService, carrierProfileService, transferManifestService, complianceCheckService, db, redisClient)

	engine.GET("/healthz", systemHandler.Health)
	engine.POST("/api/auth/login", systemHandler.Login)

	limiter := middleware.NewLimiter(redisClient, cfg.RequestLimit)
	api := engine.Group("/api")
	api.Use(limiter.Middleware(), middleware.Authenticate(cfg))
	api.GET("/overview", systemHandler.Overview)
	api.GET("/audits", middleware.RequireMinimumRole(model.RoleReviewer), systemHandler.Audits)
	api.GET("/session", systemHandler.Session)
	api.GET("/runtime", systemHandler.Runtime)
	api.GET("/audit-summary", middleware.RequireMinimumRole(model.RoleReviewer), systemHandler.AuditSummary)
	api.GET("/audits/:entityType/:id", middleware.RequireMinimumRole(model.RoleReviewer), systemHandler.EntityHistory)
	wasteGeneratorHandler.Register(api)
	carrierProfileHandler.Register(api)
	transferManifestHandler.Register(api)
	complianceCheckHandler.Register(api)
	qualificationSnapshotHandler.Register(api)

	engine.NoRoute(func(c *gin.Context) {
		if c.Request.Method == http.MethodOptions {
			c.Status(http.StatusNoContent)
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"error": "route_not_found"})
	})
	return engine
}
