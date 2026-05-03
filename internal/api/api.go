package api

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/sessions"
	"github.com/invopop/ctxi18n"
	"github.com/invopop/ctxi18n/i18n"
	"github.com/kakra/ferry/ent"
	"github.com/kakra/ferry/internal/cleanup"
	"github.com/kakra/ferry/internal/config"
	"github.com/kakra/ferry/internal/storage"
	"github.com/kakra/ferry/internal/upload"
	"github.com/labstack/echo-contrib/session"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"golang.org/x/time/rate"
)

const (
	sessionName           = "ferry_session"
	guestSessionPrefix    = "unlocked_"
	guestUnlockVersionKey = "unlock_version_"
	uploadSessionIDPrefix = "upload_sid_"
	userSessionKey        = "user_id"
	breakGlassSessionKey  = "breakglass_authenticated"
	maxUploadSuccessIDs   = 200
)

// Server owns the HTTP router, middleware, and application dependencies.
type Server struct {
	echo       *echo.Echo
	config     *config.Config
	db         *ent.Client
	cleanup    *cleanup.Worker
	upload     *upload.Manager
	storage    storage.Storage
	logger     *MemoryLogger
	breakGlass bool
	SkipCSRF   bool // Used for testing
}

// NewServer constructs the normal application server.
func NewServer(cfg *config.Config, dbClient *ent.Client, cleanupWorker *cleanup.Worker, uploadMgr *upload.Manager, st storage.Storage) *Server {
	return NewServerWithLogger(cfg, dbClient, cleanupWorker, uploadMgr, st, nil)
}

// NewServerWithLogger constructs the application server and exposes recent logs when provided.
func NewServerWithLogger(cfg *config.Config, dbClient *ent.Client, cleanupWorker *cleanup.Worker, uploadMgr *upload.Manager, st storage.Storage, ml *MemoryLogger) *Server {
	e := echo.New()

	s := &Server{
		echo:    e,
		config:  cfg,
		db:      dbClient,
		cleanup: cleanupWorker,
		upload:  uploadMgr,
		storage: st,
		logger:  ml,
	}

	if cfg != nil && !cfg.Security.BehindReverseProxy {
		log.Println("⚠️  WARNING: security.behind_reverse_proxy is set to FALSE.")
		log.Println("   This is unsafe for production. Session cookies will be sent over unencrypted HTTP.")
	}

	// Trust Proxies
	if cfg != nil && len(cfg.Server.TrustedProxies) > 0 {
		var trustOptions []echo.TrustOption
		for _, p := range cfg.Server.TrustedProxies {
			_, ipNet, err := net.ParseCIDR(p)
			if err == nil {
				trustOptions = append(trustOptions, echo.TrustIPRange(ipNet))
			} else {
				ip := net.ParseIP(p)
				if ip != nil {
					// Convert single IP to CIDR range
					mask := net.CIDRMask(128, 128)
					if ip.To4() != nil {
						mask = net.CIDRMask(32, 32)
					}
					trustOptions = append(trustOptions, echo.TrustIPRange(&net.IPNet{IP: ip, Mask: mask}))
				}
			}
		}
		e.IPExtractor = echo.ExtractIPFromRealIPHeader(trustOptions...)
	}

	// Middleware
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Use(s.securityHeadersMiddleware())

	// CSRF Protection
	e.Use(middleware.CSRFWithConfig(middleware.CSRFConfig{
		TokenLookup: "header:X-CSRF-Token,form:csrf",
		Skipper: func(c echo.Context) bool {
			// Skip CSRF if requested (e.g. for testing)
			if s.SkipCSRF {
				return true
			}
			// Skip CSRF for health check
			if c.Path() == "/health" {
				return true
			}
			// Skip CSRF only for the dedicated maintenance API route when it is
			// authenticated via the shared Bearer secret.
			if s.isMaintenanceBearerRequest(c) {
				return true
			}
			// Skip CSRF for TUS uploads
			if strings.HasPrefix(c.Path(), "/api/upload") {
				return true
			}
			return false
		},
	}))

	// Locale Middleware
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if c.Request() == nil {
				return next(c)
			}
			lang := c.Request().Header.Get("Accept-Language")
			ctx, _ := ctxi18n.WithLocale(c.Request().Context(), lang)
			c.SetRequest(c.Request().WithContext(ctx))
			return next(c)
		}
	})

	if cfg != nil {
		e.Use(session.Middleware(sessions.NewCookieStore([]byte(cfg.Security.SessionSecret))))
	}

	// Templates
	e.Renderer = NewTemplateRenderer("web/templates", s)

	s.routes()

	return s
}

func (s *Server) isMaintenanceBearerRequest(c echo.Context) bool {
	if c.Path() != "/api/admin/cleanup" {
		return false
	}
	authHeader := c.Request().Header.Get("Authorization")
	return strings.HasPrefix(authHeader, "Bearer ")
}

// NewBreakGlassServer constructs the restricted local recovery server.
func NewBreakGlassServer(cfg *config.Config, dbClient *ent.Client, st storage.Storage) *Server {
	s := NewServer(cfg, dbClient, nil, nil, st)
	s.breakGlass = true
	return s
}

func (s *Server) authRateLimiter() echo.MiddlewareFunc {
	return middleware.RateLimiterWithConfig(middleware.RateLimiterConfig{
		Skipper: func(c echo.Context) bool {
			return !s.config.Auth.RateLimit.Enabled || c.Request().Method != http.MethodPost
		},
		Store: middleware.NewRateLimiterMemoryStoreWithConfig(middleware.RateLimiterMemoryStoreConfig{
			Rate:      rate.Limit(s.config.Auth.RateLimit.Rate),
			Burst:     s.config.Auth.RateLimit.Burst,
			ExpiresIn: 24 * time.Hour,
		}),
		IdentifierExtractor: func(ctx echo.Context) (string, error) {
			id := ctx.RealIP()
			return id, nil
		},
		DenyHandler: func(c echo.Context, identifier string, err error) error {
			return echo.NewHTTPError(http.StatusTooManyRequests, "Too many attempts. Please try again later.")
		},
	})
}

func (s *Server) securityHeadersMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			h := c.Response().Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "SAMEORIGIN")
			h.Set("Referrer-Policy", "same-origin")
			h.Set("Content-Security-Policy", "default-src 'self'; base-uri 'self'; frame-ancestors 'self'; script-src 'self' https://unpkg.com 'unsafe-inline' 'unsafe-eval'; style-src 'self' https: http: 'unsafe-inline'; img-src 'self' https: http: data:")
			return next(c)
		}
	}
}

func (s *Server) routes() {
	// Public routes
	s.echo.GET("/login", s.handleLogin)
	s.echo.POST("/login", s.handleLoginPost, s.authRateLimiter())
	s.echo.GET("/logout", s.handleLogout)
	s.echo.GET("/health", s.handleHealth)
	s.echo.GET("/setup", s.handleSetup)
	s.echo.POST("/setup", s.handleSetupPost, s.authRateLimiter())
	s.echo.GET("/s/:token", s.handlePublicShare)
	s.echo.GET("/s/:token/files-fragment", s.handleGetFilesFragment)
	s.echo.GET("/s/:token/unlock", s.handlePublicShareUnlock)
	s.echo.POST("/s/:token/unlock", s.handlePublicShareUnlockPost, s.authRateLimiter())
	s.echo.GET("/s/:token/files/:id/download", s.handleFileDownload)
	s.echo.DELETE("/s/:token/files/:id", s.handleFileDelete)

	// Fixed TUS Upload Route
	// We use a wildcard and let TUS handle its own trailing slash requirements
	s.echo.Any("/api/upload/*", s.handleTUSUpload)
	s.echo.Any("/api/upload", s.handleTUSUpload)
	s.echo.GET("/api/upload/status/clear", func(c echo.Context) error { return c.NoContent(http.StatusOK) })
	s.echo.GET("/api/upload/status/:uploadId", s.handleGetUploadStatus)

	// Admin routes (Protected)
	admin := s.echo.Group("")
	admin.Use(s.authMiddleware)
	admin.GET("/", s.handleIndex)
	admin.GET("/create", s.handleCreateView)
	admin.GET("/status", s.handleStatus, s.adminOnlyMiddleware)
	admin.GET("/admin/logs", s.handleLogs, s.adminOnlyMiddleware)
	admin.POST("/api/shares", s.handleCreateShare)
	admin.GET("/admin/shares/:id/prepare", s.handleAdminSharePrepare)
	admin.POST("/admin/shares/:id/details", s.handleUpdateShareDetails)
	admin.POST("/admin/shares/:id/password", s.handleRotateSharePassword)
	admin.POST("/admin/shares/:id/delete", s.handleDeleteShare)
	admin.POST("/admin/users", s.handleCreateUser, s.adminOnlyMiddleware)
	admin.POST("/admin/users/:id/toggle-disabled", s.handleToggleUserDisabled, s.adminOnlyMiddleware)
	admin.POST("/admin/users/:id/password", s.handleResetUserPassword, s.adminOnlyMiddleware)
	admin.POST("/admin/users/:id/delete", s.handleDeleteUser, s.adminOnlyMiddleware)
	admin.POST("/admin/users/:id/permissions/:perm", s.handleUpdateUserPermission, s.adminOnlyMiddleware)

	// API Admin routes (Protected with either session OR Bearer token)
	apiAdmin := s.echo.Group("/api/admin")
	apiAdmin.Use(s.apiAuthMiddleware)
	apiAdmin.POST("/cleanup", s.handleAdminCleanup)
}

func (s *Server) handleHealth(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// Start begins serving HTTP requests on the configured listener.
func (s *Server) Start() error {
	return s.echo.Start(s.config.Server.Listen)
}

func (s *Server) translate(c echo.Context, key string, args ...interface{}) string {
	ctx := c.Request().Context()
	var val string
	if len(args) > 0 {
		params := make(i18n.M)
		for i := 0; i < len(args); i += 2 {
			if i+1 < len(args) {
				if k, ok := args[i].(string); ok {
					// Ensure enums/etc are stringified
					params[k] = fmt.Sprint(args[i+1])
				}
			}
		}
		val = i18n.T(ctx, key, params)
	} else {
		val = i18n.T(ctx, key)
	}

	// LOG MISSING KEYS (Internal visibility for dev/admin)
	if val == key {
		localeCode := "unknown"
		if loc := ctxi18n.Locale(ctx); loc != nil {
			localeCode = loc.Code().String()
		}
		log.Printf("i18n: Missing translation key '%s' for locale '%s'", key, localeCode)
	}
	return val
}
