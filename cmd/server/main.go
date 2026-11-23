package main

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"rapid-screening/pkg/api"
	"rapid-screening/pkg/geo"
	"rapid-screening/pkg/providers"
	"rapid-screening/pkg/store"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	loadDotEnv(".env", ".env.local")

	port := getEnvString("PORT", "8080")
	dataFile := resolveDataFile(os.Getenv("DATA_FILE"))
	cacheTTL := time.Duration(getEnvInt("CACHE_TTL_MINUTES", 15)) * time.Minute
	refreshCooldown := time.Duration(getEnvInt("REFRESH_COOLDOWN_SECONDS", 60)) * time.Second
	corsOrigins := splitCSV(getEnvString("CORS_ALLOWED_ORIGINS", "http://localhost:5173,http://localhost:3000"))
	rateLimitRPM := getEnvInt("RATE_LIMIT_RPM", 120)
	rateLimitBurst := getEnvInt("RATE_LIMIT_BURST", 30)
	webhookSecret := strings.TrimSpace(os.Getenv("WEBHOOK_SECRET"))
	track17Secret := strings.TrimSpace(os.Getenv("TRACK17_WEBHOOK_SECRET"))
	tmSecret := strings.TrimSpace(os.Getenv("TRACKINGMORE_WEBHOOK_SECRET"))
	adminToken := strings.TrimSpace(os.Getenv("ADMIN_TOKEN"))

	trackingStore, err := store.NewTrackingStore(dataFile, cacheTTL, refreshCooldown)
	if err != nil {
		logger.Error("failed_to_initialize_store", "data_file", dataFile, "error", err)
		os.Exit(1)
	}

	enricher := geo.NewCatalogEnricher()
	chain := providers.NewDefaultChain()

	srv := api.NewServer(trackingStore, enricher, chain, api.ServerConfig{
		AllowedOrigins:            corsOrigins,
		RateLimitRPM:              rateLimitRPM,
		RateLimitBurst:            rateLimitBurst,
		WebhookSecret:             webhookSecret,
		Track17WebhookSecret:      track17Secret,
		TrackingMoreWebhookSecret: tmSecret,
		AdminToken:                adminToken,
		Logger:                    logger,
	})

	httpServer := &http.Server{
		Addr:              ":" + port,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		stats := trackingStore.Stats()
		logger.Info("rapid_screening_server_starting",
			"addr", httpServer.Addr,
			"data_file", dataFile,
			"backend_mode", stats.BackendMode,
			"stored_count", stats.StoredCount,
			"providers", len(chain.Statuses()),
		)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-errCh:
		if err != nil {
			logger.Error("http_server_failed", "error", err)
			os.Exit(1)
		}
	case <-sigCtx.Done():
		logger.Info("shutdown_signal_received")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful_shutdown_failed", "error", err)
			os.Exit(1)
		}
		logger.Info("rapid_screening_server_stopped")
	}
}

func resolveDataFile(configured string) string {
	trimmed := strings.TrimSpace(configured)
	if trimmed != "" {
		return trimmed
	}
	if info, err := os.Stat("data"); err == nil && info.IsDir() {
		return "data/trackings.json"
	}
	return "../data/trackings.json"
}

func loadDotEnv(candidates ...string) {
	for _, path := range candidates {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			line = strings.TrimPrefix(line, "export ")
			idx := strings.IndexByte(line, '=')
			if idx <= 0 {
				continue
			}
			key := strings.TrimSpace(line[:idx])
			val := strings.TrimSpace(line[idx+1:])
			if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
				val = val[1 : len(val)-1]
			}
			if key == "" {
				continue
			}
			if _, exists := os.LookupEnv(key); !exists {
				_ = os.Setenv(key, val)
			}
		}
		_ = f.Close()
		return
	}
}

func getEnvString(key, fallback string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		return fallback
	}
	return v
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
