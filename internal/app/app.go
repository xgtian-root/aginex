package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/xgtian-root/aginex/internal/auth"
	"github.com/xgtian-root/aginex/internal/config"
	"github.com/xgtian-root/aginex/internal/domain"
	"github.com/xgtian-root/aginex/internal/platform/migrate"
	"github.com/xgtian-root/aginex/internal/platform/storage"
	"gorm.io/gorm"
)

const principalKey = "aginex.principal"

type App struct {
	cfg     config.Config
	db      *gorm.DB
	auth    *auth.Service
	store   storage.Storage
	http    *gin.Engine
	openapi *huma.OpenAPI
}

type healthOutput struct {
	Body struct {
		Status string    `json:"status" example:"ok"`
		Time   time.Time `json:"time"`
	}
}

func New(cfg config.Config, db *gorm.DB) (*App, error) {
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	if err := migrate.Up(sqlDB, cfg.Database.Driver); err != nil {
		return nil, err
	}
	if err := bootstrap(db, cfg.Bootstrap); err != nil {
		return nil, err
	}
	store, err := storage.FromConfig(context.Background(), cfg.Storage, cfg.HTTP.PublicURL)
	if err != nil {
		return nil, fmt.Errorf("configure storage: %w", err)
	}

	instance := &App{cfg: cfg, db: db, auth: auth.New(db, cfg.Session), store: store}
	instance.http = instance.routes()
	return instance, nil
}

func (a *App) Handler() http.Handler {
	return a.http
}

func (a *App) OpenAPI() *huma.OpenAPI {
	return a.openapi
}

func (a *App) routes() *gin.Engine {
	if a.cfg.Environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	}
	router := gin.New()
	router.Use(gin.Recovery(), a.requestContext(), a.cors(), a.sameOrigin())

	api := humagin.New(router, huma.DefaultConfig("Aginex API", "1.0.0"))
	a.openapi = api.OpenAPI()
	huma.Get(api, "/api/v1/health/live", func(_ context.Context, _ *struct{}) (*healthOutput, error) {
		output := &healthOutput{}
		output.Body.Status = "ok"
		output.Body.Time = time.Now().UTC()
		return output, nil
	})
	huma.Get(api, "/api/v1/health/ready", func(_ context.Context, _ *struct{}) (*healthOutput, error) {
		sqlDB, err := a.db.DB()
		if err != nil || sqlDB.Ping() != nil {
			return nil, huma.Error503ServiceUnavailable("database is unavailable")
		}
		output := &healthOutput{}
		output.Body.Status = "ready"
		output.Body.Time = time.Now().UTC()
		return output, nil
	})
	documentGinOperations(api.OpenAPI())

	v1 := router.Group("/api/v1")
	v1.POST("/auth/login", a.login)
	v1.POST("/auth/logout", a.authenticate(), a.logout)
	v1.GET("/auth/me", a.authenticate(), a.me)

	protected := v1.Group("")
	protected.Use(a.authenticate())
	protected.GET("/products", a.require("products:read"), a.listProducts)
	protected.POST("/products", a.require("products:create"), a.createProduct)
	protected.GET("/products/:id", a.require("products:read"), a.getProduct)
	protected.PUT("/products/:id", a.require("products:update"), a.updateProduct)
	protected.DELETE("/products/:id", a.require("products:delete"), a.deleteProduct)
	protected.GET("/users", a.require("users:read"), a.listUsers)
	protected.GET("/roles", a.require("roles:read"), a.listRoles)
	protected.GET("/permissions", a.require("roles:read"), a.listPermissions)
	protected.GET("/audit-logs", a.require("audit:read"), a.listAuditLogs)
	protected.GET("/dashboard/summary", a.require("dashboard:read"), a.dashboardSummary)
	protected.GET("/files", a.require("files:read"), a.listFiles)
	protected.POST("/files/upload-intents", a.require("files:create"), a.createUploadIntent)
	protected.PUT("/files/local-upload/*key", a.require("files:create"), a.localUpload)
	protected.GET("/files/local-content/*key", a.require("files:read"), a.localContent)
	protected.POST("/files/:id/confirm", a.require("files:create"), a.confirmUpload)
	protected.GET("/files/:id/url", a.require("files:read"), a.fileURL)
	protected.DELETE("/files/:id", a.require("files:delete"), a.deleteFile)
	return router
}

func (a *App) requestContext() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := strings.TrimSpace(c.GetHeader("X-Request-ID"))
		if requestID == "" {
			buf := make([]byte, 12)
			if _, err := rand.Read(buf); err == nil {
				requestID = hex.EncodeToString(buf)
			}
		}
		c.Header("X-Request-ID", requestID)
		started := time.Now()
		c.Next()
		slog.Info("HTTP request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"duration", time.Since(started),
			"request_id", requestID,
		)
	}
}

func (a *App) cors() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin == a.cfg.WebOrigin {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Headers", "Content-Type, X-Request-ID")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func (a *App) sameOrigin() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}
		origin := c.GetHeader("Origin")
		if origin != "" && origin != a.cfg.WebOrigin {
			writeProblem(c, http.StatusForbidden, "Cross-origin request denied", "The request origin is not allowed.")
			c.Abort()
			return
		}
		c.Next()
	}
}

func (a *App) authenticate() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie(a.cfg.Session.CookieName)
		if err != nil {
			writeProblem(c, http.StatusUnauthorized, "Authentication required", "Sign in to continue.")
			c.Abort()
			return
		}
		principal, err := a.auth.Authenticate(token)
		if err != nil {
			writeProblem(c, http.StatusUnauthorized, "Session expired", "Sign in again to continue.")
			c.Abort()
			return
		}
		c.Set(principalKey, principal)
		c.Next()
	}
}

func (a *App) require(permission string) gin.HandlerFunc {
	return func(c *gin.Context) {
		principal := currentPrincipal(c)
		if !principal.Can(permission) {
			writeProblem(c, http.StatusForbidden, "Permission denied", "Your role does not grant "+permission+".")
			c.Abort()
			return
		}
		c.Next()
	}
}

type loginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

func (a *App) login(c *gin.Context) {
	var input loginRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		writeProblem(c, http.StatusBadRequest, "Invalid sign-in request", err.Error())
		return
	}
	token, user, err := a.auth.Login(input.Email, input.Password, c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		writeProblem(c, http.StatusUnauthorized, "Sign-in failed", "The email or password is incorrect.")
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     a.cfg.Session.CookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(a.cfg.Session.TTL.Seconds()),
		HttpOnly: true,
		Secure:   a.cfg.Session.Secure,
		SameSite: http.SameSiteLaxMode,
	})
	a.audit(c, &user.ID, "auth:login", "session", "", "User signed in")
	c.JSON(http.StatusOK, userView(user, nil))
}

func (a *App) logout(c *gin.Context) {
	token, _ := c.Cookie(a.cfg.Session.CookieName)
	principal := currentPrincipal(c)
	if err := a.auth.Logout(token); err != nil {
		writeProblem(c, http.StatusInternalServerError, "Sign-out failed", "The session could not be revoked.")
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name: a.cfg.Session.CookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: a.cfg.Session.Secure, SameSite: http.SameSiteLaxMode,
	})
	a.audit(c, &principal.User.ID, "auth:logout", "session", principal.SessionID, "User signed out")
	c.Status(http.StatusNoContent)
}

func (a *App) me(c *gin.Context) {
	principal := currentPrincipal(c)
	permissions := make([]string, 0, len(principal.Permissions))
	for code := range principal.Permissions {
		permissions = append(permissions, code)
	}
	c.JSON(http.StatusOK, userView(principal.User, permissions))
}

type productInput struct {
	Name       string `json:"name" binding:"required,min=2,max=240"`
	SKU        string `json:"sku" binding:"required,min=2,max=120"`
	PriceCents int64  `json:"priceCents" binding:"min=0"`
	Status     string `json:"status" binding:"required,oneof=draft active archived"`
}

func (a *App) listProducts(c *gin.Context) {
	page, pageSize := pagination(c)
	query := a.db.Model(&domain.Product{})
	if search := strings.TrimSpace(c.Query("search")); search != "" {
		like := "%" + search + "%"
		query = query.Where("name LIKE ? OR sku LIKE ?", like, like)
	}
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		query = query.Where("status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		writeProblem(c, http.StatusInternalServerError, "Products unavailable", err.Error())
		return
	}
	var products []domain.Product
	if err := query.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&products).Error; err != nil {
		writeProblem(c, http.StatusInternalServerError, "Products unavailable", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": products, "page": page, "pageSize": pageSize, "total": total})
}

func (a *App) createProduct(c *gin.Context) {
	var input productInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeProblem(c, http.StatusBadRequest, "Invalid product", err.Error())
		return
	}
	now := time.Now().UTC()
	product := domain.Product{
		ID: uuid.NewString(), Name: strings.TrimSpace(input.Name), SKU: strings.TrimSpace(input.SKU),
		PriceCents: input.PriceCents, Status: input.Status, CreatedAt: now, UpdatedAt: now,
	}
	if err := a.db.Create(&product).Error; err != nil {
		writeProblem(c, http.StatusConflict, "Product could not be created", err.Error())
		return
	}
	principal := currentPrincipal(c)
	a.audit(c, &principal.User.ID, "products:create", "product", product.ID, "Created "+product.SKU)
	c.Header("Location", "/api/v1/products/"+product.ID)
	c.JSON(http.StatusCreated, product)
}

func (a *App) getProduct(c *gin.Context) {
	var product domain.Product
	if err := a.db.First(&product, "id = ?", c.Param("id")).Error; err != nil {
		notFoundOrInternal(c, "Product", err)
		return
	}
	c.JSON(http.StatusOK, product)
}

func (a *App) updateProduct(c *gin.Context) {
	var input productInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeProblem(c, http.StatusBadRequest, "Invalid product", err.Error())
		return
	}
	var product domain.Product
	if err := a.db.First(&product, "id = ?", c.Param("id")).Error; err != nil {
		notFoundOrInternal(c, "Product", err)
		return
	}
	product.Name = strings.TrimSpace(input.Name)
	product.SKU = strings.TrimSpace(input.SKU)
	product.PriceCents = input.PriceCents
	product.Status = input.Status
	product.UpdatedAt = time.Now().UTC()
	if err := a.db.Save(&product).Error; err != nil {
		writeProblem(c, http.StatusConflict, "Product could not be updated", err.Error())
		return
	}
	principal := currentPrincipal(c)
	a.audit(c, &principal.User.ID, "products:update", "product", product.ID, "Updated "+product.SKU)
	c.JSON(http.StatusOK, product)
}

func (a *App) deleteProduct(c *gin.Context) {
	var product domain.Product
	if err := a.db.First(&product, "id = ?", c.Param("id")).Error; err != nil {
		notFoundOrInternal(c, "Product", err)
		return
	}
	if err := a.db.Delete(&product).Error; err != nil {
		writeProblem(c, http.StatusInternalServerError, "Product could not be deleted", err.Error())
		return
	}
	principal := currentPrincipal(c)
	a.audit(c, &principal.User.ID, "products:delete", "product", product.ID, "Deleted "+product.SKU)
	c.Status(http.StatusNoContent)
}

func (a *App) listUsers(c *gin.Context) {
	page, pageSize := pagination(c)
	var total int64
	a.db.Model(&domain.User{}).Count(&total)
	var users []domain.User
	if err := a.db.Preload("Roles").Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&users).Error; err != nil {
		writeProblem(c, http.StatusInternalServerError, "Users unavailable", err.Error())
		return
	}
	items := make([]gin.H, 0, len(users))
	for _, user := range users {
		roles := make([]string, 0, len(user.Roles))
		for _, role := range user.Roles {
			roles = append(roles, role.Name)
		}
		items = append(items, userView(user, roles))
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "page": page, "pageSize": pageSize, "total": total})
}

func (a *App) listRoles(c *gin.Context) {
	var roles []domain.Role
	if err := a.db.Preload("Permissions").Order("name").Find(&roles).Error; err != nil {
		writeProblem(c, http.StatusInternalServerError, "Roles unavailable", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": roles, "page": 1, "pageSize": len(roles), "total": len(roles)})
}

func (a *App) listPermissions(c *gin.Context) {
	var permissions []domain.Permission
	if err := a.db.Order("code").Find(&permissions).Error; err != nil {
		writeProblem(c, http.StatusInternalServerError, "Permissions unavailable", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": permissions, "page": 1, "pageSize": len(permissions), "total": len(permissions)})
}

func (a *App) listAuditLogs(c *gin.Context) {
	page, pageSize := pagination(c)
	var total int64
	a.db.Model(&domain.AuditLog{}).Count(&total)
	var logs []domain.AuditLog
	if err := a.db.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&logs).Error; err != nil {
		writeProblem(c, http.StatusInternalServerError, "Audit history unavailable", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": logs, "page": page, "pageSize": pageSize, "total": total})
}

func (a *App) dashboardSummary(c *gin.Context) {
	var productCount, userCount, eventCount int64
	a.db.Model(&domain.Product{}).Count(&productCount)
	a.db.Model(&domain.User{}).Count(&userCount)
	a.db.Model(&domain.AuditLog{}).Where("created_at >= ?", time.Now().UTC().Add(-24*time.Hour)).Count(&eventCount)
	c.JSON(http.StatusOK, gin.H{
		"products":          productCount,
		"users":             userCount,
		"eventsLast24Hours": eventCount,
		"generatedAt":       time.Now().UTC(),
	})
}

func (a *App) audit(c *gin.Context, actorID *string, action, resource, resourceID, summary string) {
	entry := domain.AuditLog{
		ID: uuid.NewString(), ActorID: actorID, Action: action, Resource: resource, ResourceID: resourceID,
		Summary: summary, RequestID: c.Writer.Header().Get("X-Request-ID"), IPAddress: c.ClientIP(), CreatedAt: time.Now().UTC(),
	}
	if err := a.db.Create(&entry).Error; err != nil {
		slog.Error("write audit log", "error", err, "action", action, "resource_id", resourceID)
	}
}

func currentPrincipal(c *gin.Context) auth.Principal {
	value, _ := c.Get(principalKey)
	principal, _ := value.(auth.Principal)
	return principal
}

func pagination(c *gin.Context) (int, int) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize
}

func userView(user domain.User, permissionsOrRoles []string) gin.H {
	return gin.H{
		"id": user.ID, "email": user.Email, "displayName": user.DisplayName,
		"status": user.Status, "permissions": permissionsOrRoles, "createdAt": user.CreatedAt,
	}
}

func notFoundOrInternal(c *gin.Context, resource string, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		writeProblem(c, http.StatusNotFound, resource+" not found", fmt.Sprintf("No %s matches this identifier.", strings.ToLower(resource)))
		return
	}
	writeProblem(c, http.StatusInternalServerError, resource+" unavailable", err.Error())
}

func writeProblem(c *gin.Context, status int, title, detail string) {
	c.Header("Content-Type", "application/problem+json")
	c.JSON(status, gin.H{
		"type": "about:blank", "title": title, "status": status, "detail": detail,
		"instance": c.Request.URL.Path, "requestId": c.Writer.Header().Get("X-Request-ID"),
	})
}
