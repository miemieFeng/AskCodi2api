package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jonasen/askcodi-go/internal/config"
	"github.com/jonasen/askcodi-go/internal/database"
	"github.com/jonasen/askcodi-go/internal/handler"
	"github.com/jonasen/askcodi-go/internal/middleware"
	"github.com/jonasen/askcodi-go/internal/service"
)

func main() {
	cfg := config.Load()

	// Initialize database
	db, err := database.Open(cfg.DatabasePath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	if err := database.RunMigrations(db); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}
	log.Println("Database initialized successfully")

	// Initialize services
	logger := service.NewLogger(200)
	proxyMgr := service.NewProxyManager(db)
	acctMgr := service.NewAccountManager(db)
	regSvc := service.NewRegistrationService(db, proxyMgr, logger)
	askcodiClient := service.NewAskCodiClient(db, acctMgr, proxyMgr)

	// Initialize handlers
	healthH := &handler.HealthHandler{}
	dashH := handler.NewDashboardHandler(db, regSvc, proxyMgr, logger)
	chatH := handler.NewChatHandler(askcodiClient)

	// Router
	r := chi.NewRouter()
	r.Use(middleware.CORS)

	// Health
	r.Get("/api/health", healthH.Health)

	// Dashboard API
	r.Get("/api/dashboard/stats", dashH.GetStats)
	r.Get("/api/accounts", dashH.GetAccounts)
	r.Post("/api/accounts/register", dashH.TriggerRegistration)
	r.Post("/api/accounts/{id}/refresh", dashH.RefreshAccount)
	r.Post("/api/accounts/refresh_all", dashH.RefreshAllTokens)
	r.Post("/api/accounts/{id}/disable", dashH.DisableAccount)
	r.Delete("/api/accounts/{id}", dashH.DeleteAccount)
	r.Get("/api/proxies", dashH.GetProxies)
	r.Post("/api/proxies", dashH.AddProxy)
	r.Post("/api/proxies/refresh-zenproxy", dashH.RefreshZenProxies)
	r.Delete("/api/proxies/{id}", dashH.DeleteProxy)
	r.Get("/api/config", dashH.GetConfig)
	r.Put("/api/config", dashH.UpdateConfig)
	r.Get("/api/registration/logs", dashH.GetRegistrationLogs)

	// Chat API (OpenAI / Anthropic compatible)
	r.Get("/v1/models", chatH.GetModels)
	r.Post("/v1/chat/completions", chatH.ChatCompletions)
	r.Post("/v1/messages", chatH.AnthropicMessages)

	// Static files for UI
	staticDir := http.Dir("static")
	fileServer := http.FileServer(staticDir)
	r.Handle("/ui/*", http.StripPrefix("/ui", fileServer))
	r.Handle("/ui", http.RedirectHandler("/ui/", http.StatusMovedPermanently))

	// Start background worker
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go service.StartBackgroundWorker(ctx, db, regSvc, proxyMgr, acctMgr, logger)

	// Start server
	srv := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: r,
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down...")
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		srv.Shutdown(shutdownCtx)
	}()

	fmt.Printf("AskCodi Token Pool Manager (Go) listening on %s\n", cfg.ListenAddr)
	fmt.Printf("Dashboard: http://localhost%s/ui/\n", cfg.ListenAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}
