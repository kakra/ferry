package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/invopop/ctxi18n"
	"github.com/invopop/ctxi18n/i18n"
	"github.com/kakra/ferry/internal/api"
	"github.com/kakra/ferry/internal/cleanup"
	"github.com/kakra/ferry/internal/config"
	"github.com/kakra/ferry/internal/db"
	internalI18n "github.com/kakra/ferry/internal/i18n"
	"github.com/kakra/ferry/internal/storage"
	"github.com/kakra/ferry/internal/upload"
)

func main() {
	if err := internalI18n.Init(); err != nil {
		log.Fatalf("Failed to initialize i18n: %v", err)
	}

	lang := os.Getenv("LANG")
	if lang == "" {
		lang = os.Getenv("LC_ALL")
	}
	ctx, _ := ctxi18n.WithLocale(context.Background(), lang)
	l := i18n.GetLocale(ctx)

	command := commandFromArgs(os.Args[1:])

	switch command {
	case "serve":
		runServe(ctx, l)
	case "init-config":
		runInitConfig()
	case "break-glass":
		runBreakGlass(ctx, l)
	case "cleanup":
		runCleanup(ctx, l)
	case "migrate":
		runMigrate(ctx, l)
	case "help", "-h", "--help":
		fmt.Printf("Usage: ferry [command] [options]\n\nCommands:\n  serve        Start the web server and internal worker (default)\n  init-config  Create or repair config.yaml with generated secrets\n  break-glass  Start the local recovery server on loopback only\n  cleanup      Trigger an immediate GC run via API\n  migrate      Run database migrations\n\nOptions:\n  --dev        Allow development defaults for local-only runs\n")
	default:
		log.Fatalf("Unknown command: %s. Use 'help' for usage.", command)
	}
}

func runServe(ctx context.Context, l *i18n.Locale) {
	if hasCLIFlag("--dev") {
		_ = os.Setenv("FERRY_DEV_MODE", "true")
		log.Printf("DEV MODE ACTIVE: insecure default secrets are allowed. Do not use this mode for production.")
	}

	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	client, err := db.NewClient(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer client.Close()
	log.Println(l.T("cli.db_init", i18n.M{"path": cfg.Database.Path}))

	st, err := storage.NewFileStorage(cfg.Storage.Path)
	if err != nil {
		log.Fatalf("Failed to initialize storage: %v", err)
	}

	cleanupWorker := cleanup.NewWorker(cfg, client, st)
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go cleanupWorker.Start(workerCtx)

	// Keep a bounded log buffer for the status page.
	ml := api.NewMemoryLogger(100)
	multiWriter := io.MultiWriter(os.Stderr, ml)
	log.SetOutput(multiWriter)

	uploadMgr, err := upload.NewManager(cfg, client, st)
	if err != nil {
		log.Fatalf("Failed to initialize upload manager: %v", err)
	}
	go uploadMgr.StartHookListener(workerCtx)

	srv := api.NewServerWithLogger(cfg, client, cleanupWorker, uploadMgr, st, ml)

	go func() {
		if err := srv.Start(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down ferry...")
}

func runInitConfig() {
	result, err := config.InitConfig("config.yaml")
	if err != nil {
		log.Fatalf("Failed to initialize config: %v", err)
	}
	switch {
	case result.Created:
		fmt.Println("Created config.yaml with generated secrets.")
	case result.Updated:
		fmt.Println("Updated config.yaml with generated secrets.")
	default:
		fmt.Println("config.yaml already contains non-default secrets.")
	}
}

func runBreakGlass(ctx context.Context, l *i18n.Locale) {
	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if cfg.Auth.BootstrapPassword == "" {
		log.Fatalf("Break-glass mode requires auth.bootstrap_password to be set")
	}

	cfg.Server.Listen = "127.0.0.1:8081"

	client, err := db.NewClient(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer client.Close()
	log.Println(l.T("cli.db_init", i18n.M{"path": cfg.Database.Path}))

	st, err := storage.NewFileStorage(cfg.Storage.Path)
	if err != nil {
		log.Fatalf("Failed to initialize storage: %v", err)
	}

	log.Printf("BREAK-GLASS MODE ACTIVE: listening on http://%s", cfg.Server.Listen)
	log.Printf("BREAK-GLASS MODE ACTIVE: public shares, uploads, and normal cleanup endpoints are disabled")

	srv := api.NewBreakGlassServer(cfg, client, st)

	go func() {
		if err := srv.Start(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start break-glass server: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down break-glass ferry...")
}

func runCleanup(ctx context.Context, l *i18n.Locale) {
	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Create request with Bearer token authentication
	url := fmt.Sprintf("http://%s/api/admin/cleanup", cfg.Server.Listen)
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		log.Fatalf("Failed to create request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Auth.StaticPassword)

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // Don't follow redirects to /login
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("Failed to trigger cleanup: %v. Is the server running?", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Fatalf("Server returned error: %d (Auth failure or server not running)", resp.StatusCode)
	}

	fmt.Println("Cleanup triggered successfully.")
}

func hasCLIFlag(name string) bool {
	for _, arg := range os.Args[1:] {
		if arg == name {
			return true
		}
	}
	return false
}

func commandFromArgs(args []string) string {
	for _, arg := range args {
		if arg == "help" || arg == "-h" || arg == "--help" {
			return "help"
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return arg
	}
	return "serve"
}

func runMigrate(ctx context.Context, l *i18n.Locale) {
	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	client, err := db.NewClient(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer client.Close()
	fmt.Println("Database migrated successfully.")
}
