package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/invopop/ctxi18n/i18n"
	"github.com/kakra/ferry/ent"
	"github.com/kakra/ferry/ent/blob"
	"github.com/kakra/ferry/ent/file"
	"github.com/kakra/ferry/ent/share"
	"github.com/kakra/ferry/ent/user"
	"github.com/kakra/ferry/internal/cleanup"
	"github.com/kakra/ferry/internal/config"
	"github.com/labstack/echo/v4"
)

func (s *Server) handleIndex(c echo.Context) error {
	currentFilter := dashboardFilterValue(c.QueryParam("filter"))
	currentSort := dashboardSortValue(c.QueryParam("sort"))

	// Use a safety buffer (1 minute) to avoid showing expired shares that just ticked over
	shares, err := s.db.Share.Query().
		Where(share.ExpiresAtGT(time.Now().Add(-1 * time.Minute))).
		WithFiles(func(q *ent.FileQuery) {
			q.Select(file.FieldID, file.FieldCreatedAt)
		}).
		All(c.Request().Context())
	if err != nil {
		log.Printf("Error querying shares: %v", err)
		return err
	}
	shareViews := buildDashboardShareViews(c.Request().Context(), shares, currentFilter, currentSort)

	users, err := s.db.User.Query().
		Order(ent.Asc(user.FieldUsername)).
		All(c.Request().Context())
	if err != nil {
		log.Printf("Error querying users: %v", err)
		return err
	}

	currentUser, err := s.currentUser(c)
	if err != nil {
		return err
	}

	setNoStoreHeaders(c)
	return c.Render(http.StatusOK, "index.html", map[string]interface{}{
		"UI":                s.config.UI,
		"Shares":            shareViews,
		"Users":             users,
		"CurrentUser":       currentUser,
		"BreakGlassMode":    s.breakGlass,
		"CanManageShares":   s.canManageShares(c),
		"ExpirationOptions": shareExpirationOptions(),
		"DefaultExpiration": defaultShareExpiration(s.config),
		"CurrentFilter":     currentFilter,
		"CurrentSort":       currentSort,
	})
}

func (s *Server) handleCreateView(c echo.Context) error {
	setNoStoreHeaders(c)
	return c.Render(http.StatusOK, "create.html", map[string]interface{}{
		"UI":                s.config.UI,
		"Title":             c.QueryParam("title"),
		"Note":              c.QueryParam("note"),
		"ExpirationOptions": shareExpirationOptions(),
		"DefaultExpiration": defaultShareExpiration(s.config),
	})
}

type shareExpirationOption struct {
	Value string
	Label string
}

func shareExpirationOptions() []shareExpirationOption {
	return []shareExpirationOption{
		{Value: "24h", Label: "24h"},
		{Value: "72h", Label: "3d"},
		{Value: "168h", Label: "7d"},
		{Value: "336h", Label: "14d"},
	}
}

func defaultShareExpiration(cfg *config.Config) string {
	if cfg != nil {
		for _, opt := range shareExpirationOptions() {
			if cfg.Share.DefaultExpiration == opt.Value {
				return opt.Value
			}
		}
	}
	return "168h"
}

func parseShareExpiration(raw string, cfg *config.Config) (time.Duration, error) {
	value := raw
	if value == "" {
		value = defaultShareExpiration(cfg)
	}
	for _, opt := range shareExpirationOptions() {
		if value == opt.Value {
			return time.ParseDuration(value)
		}
	}
	return 0, fmt.Errorf("unsupported share expiration: %s", value)
}

type dashboardShareView struct {
	ID                 uuid.UUID
	Title              string
	TokenHash          string
	Type               share.Type
	ExpiresAt          time.Time
	CreatedAt          time.Time
	FileCount          int
	LastActivityAt     time.Time
	LastActivityText   string
	RemainingText      string
	LifetimePercent    int
	LifetimeBarColor   string
	LifetimeBackground string
}

func dashboardFilterValue(raw string) string {
	switch raw {
	case "upload", "download":
		return raw
	default:
		return "all"
	}
}

func dashboardSortValue(raw string) string {
	switch raw {
	case "expiry", "created":
		return raw
	default:
		return "activity"
	}
}

func buildDashboardShareViews(ctx context.Context, shares []*ent.Share, filter, sortMode string) []dashboardShareView {
	views := make([]dashboardShareView, 0, len(shares))
	for _, sh := range shares {
		if filter != "all" && string(sh.Type) != filter {
			continue
		}

		lastActivity := sh.CreatedAt
		for _, f := range sh.Edges.Files {
			if f.CreatedAt.After(lastActivity) {
				lastActivity = f.CreatedAt
			}
		}
		lifetimePercent := shareLifetimePercent(sh.CreatedAt, sh.ExpiresAt, time.Now())
		barColor, barBackground := shareLifetimeColors(lifetimePercent)

		// Format using the localized date/time format
		format := "02.01.2006 15:04"
		loc := i18n.GetLocale(ctx)
		if loc != nil {
			format = string(loc.T("common.date_time_format"))
		}

		views = append(views, dashboardShareView{
			ID:                 sh.ID,
			Title:              sh.Title,
			TokenHash:          sh.TokenHash,
			Type:               sh.Type,
			ExpiresAt:          sh.ExpiresAt,
			CreatedAt:          sh.CreatedAt,
			FileCount:          len(sh.Edges.Files),
			LastActivityAt:     lastActivity,
			LastActivityText:   lastActivity.Format(format),
			RemainingText:      shareRemainingText(sh.ExpiresAt, time.Now(), ctx),
			LifetimePercent:    lifetimePercent,
			LifetimeBarColor:   barColor,
			LifetimeBackground: barBackground,
		})
	}

	sort.Slice(views, func(i, j int) bool {
		switch sortMode {
		case "expiry":
			if views[i].ExpiresAt.Equal(views[j].ExpiresAt) {
				return views[i].LastActivityAt.After(views[j].LastActivityAt)
			}
			return views[i].ExpiresAt.Before(views[j].ExpiresAt)
		case "created":
			if views[i].CreatedAt.Equal(views[j].CreatedAt) {
				return views[i].ExpiresAt.Before(views[j].ExpiresAt)
			}
			return views[i].CreatedAt.After(views[j].CreatedAt)
		default:
			if views[i].LastActivityAt.Equal(views[j].LastActivityAt) {
				return views[i].ExpiresAt.Before(views[j].ExpiresAt)
			}
			return views[i].LastActivityAt.After(views[j].LastActivityAt)
		}
	})

	return views
}

func shareLifetimePercent(createdAt, expiresAt, now time.Time) int {
	total := expiresAt.Sub(createdAt)
	if total <= 0 {
		return 0
	}
	remaining := expiresAt.Sub(now)
	if remaining <= 0 {
		return 0
	}
	if remaining >= total {
		return 100
	}
	return int((remaining * 100) / total)
}

func shareLifetimeColors(percent int) (string, string) {
	switch {
	case percent <= 15:
		return "#d32f2f", "#ffebee"
	case percent <= 40:
		return "#ef6c00", "#fff3e0"
	default:
		return "#2e7d32", "#e8f5e9"
	}
}

func shareRemainingText(expiresAt, now time.Time, ctx context.Context) string {
	remaining := expiresAt.Sub(now)
	switch {
	case remaining <= time.Hour:
		return i18n.T(ctx, "admin.remaining.soon")
	case remaining <= 24*time.Hour:
		return i18n.T(ctx, "admin.remaining.today")
	case remaining <= 48*time.Hour:
		return i18n.T(ctx, "admin.remaining.one_day")
	default:
		days := int((remaining + 23*time.Hour) / (24 * time.Hour))
		return i18n.T(ctx, "admin.remaining.days", i18n.M{"days": days})
	}
}

// StorageStats summarizes logical files, physical blobs, and disk capacity for the status page.
type StorageStats struct {
	LogicalSize     int64
	PhysicalSize    int64
	FileCount       int
	BlobCount       int
	Deduplication   float64
	LogicalSizeStr  string
	PhysicalSizeStr string
	TmpSizeStr      string
	DiskFreeStr     string
	DiskTotalStr    string
}

func (s *Server) handleLogs(c echo.Context) error {
	if s.logger == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "Logger not initialized")
	}

	logs := s.logger.GetAggregatedLogs()
	for i, j := 0, len(logs)-1; i < j; i, j = i+1, j-1 {
		logs[i], logs[j] = logs[j], logs[i]
	}

	return c.Render(http.StatusOK, "status.html", map[string]interface{}{
		"UI":          s.config.UI,
		"Logs":        logs,
		"Stats":       s.getStorageStats(c.Request().Context()),
		"WorkerStats": s.getWorkerStats(),
	})
}

func (s *Server) getStorageStats(ctx context.Context) StorageStats {
	var pSizes []int64
	_ = s.db.Blob.Query().
		Where(blob.UnreachableSinceIsNil()).
		Aggregate(ent.Sum(blob.FieldSize)).
		Scan(ctx, &pSizes)

	var physicalSize int64
	if len(pSizes) > 0 {
		physicalSize = pSizes[0]
	}

	blobCount, _ := s.db.Blob.Query().Where(blob.UnreachableSinceIsNil()).Count(ctx)

	var lSizes []int64
	_ = s.db.File.Query().
		Aggregate(func(sel *sql.Selector) string {
			t := sql.Table(blob.Table)
			sel.Join(t).On(sel.C(file.BlobColumn), t.C(blob.FieldID))
			return sql.Sum(t.C(blob.FieldSize))
		}).
		Scan(ctx, &lSizes)

	var logicalSize int64
	if len(lSizes) > 0 {
		logicalSize = lSizes[0]
	}

	fileCount, _ := s.db.File.Query().Count(ctx)

	ratio := 1.0
	if physicalSize > 0 {
		ratio = float64(logicalSize) / float64(physicalSize)
	}

	var tmpSize int64
	_ = filepath.Walk(s.storage.GetTmpPath(), func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			tmpSize += info.Size()
		}
		return nil
	})

	var free, total int64
	var fs syscall.Statfs_t
	if err := syscall.Statfs(s.config.Storage.Path, &fs); err == nil {
		free = int64(fs.Bfree) * int64(fs.Bsize)
		total = int64(fs.Blocks) * int64(fs.Bsize)
	}

	return StorageStats{
		LogicalSize:     logicalSize,
		PhysicalSize:    physicalSize,
		FileCount:       fileCount,
		BlobCount:       blobCount,
		Deduplication:   ratio,
		LogicalSizeStr:  formatBytes(logicalSize),
		PhysicalSizeStr: formatBytes(physicalSize),
		TmpSizeStr:      formatBytes(tmpSize),
		DiskFreeStr:     formatBytes(free),
		DiskTotalStr:    formatBytes(total),
	}
}

func (s *Server) getWorkerStats() cleanup.WorkerStats {
	if s.cleanup != nil {
		return s.cleanup.GetStats()
	}
	return cleanup.WorkerStats{}
}

func (s *Server) handleStatus(c echo.Context) error {
	setNoStoreHeaders(c)

	var logs []LogEntry
	if s.logger != nil {
		logs = s.logger.GetAggregatedLogs()
		// Return in reverse order (newest first)
		for i, j := 0, len(logs)-1; i < j; i, j = i+1, j-1 {
			logs[i], logs[j] = logs[j], logs[i]
		}
	}

	return c.Render(http.StatusOK, "status.html", map[string]interface{}{
		"UI":          s.config.UI,
		"Logs":        logs,
		"Stats":       s.getStorageStats(c.Request().Context()),
		"WorkerStats": s.getWorkerStats(),
	})
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

func (s *Server) handleAdminCleanup(c echo.Context) error {
	if s.cleanup == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "Cleanup worker not initialized")
	}

	ctx := c.Request().Context()
	s.cleanup.Perform(ctx)

	if c.Request().Header.Get("HX-Request") == "true" {
		loc := i18n.GetLocale(ctx)
		msg := "Cleanup triggered manually"
		if loc != nil {
			msg = string(loc.T("admin.cleanup_triggered"))
		}
		return c.HTML(http.StatusOK, fmt.Sprintf(`<div id="status-area" style="color: green;">%s</div>`, msg))
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}
