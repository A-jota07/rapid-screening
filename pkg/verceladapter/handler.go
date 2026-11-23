package verceladapter

import (
	"log/slog"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"rapid-screening/pkg/api"
	"rapid-screening/pkg/geo"
	"rapid-screening/pkg/providers"
	"rapid-screening/pkg/store"
)

var (
	once       sync.Once
	apiHandler http.Handler
	initErr    error
)

// Handler atende às invocações serverless da Vercel mantendo instâncias quentes
// de store, catálogo geográfico e cadeia de provedores via sync.Once.
func Handler(w http.ResponseWriter, r *http.Request) {
	once.Do(func() {
		apiHandler, initErr = buildHandler()
	})
	if initErr != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"serverless_init_failed","message":"Falha ao inicializar API serverless."}`))
		return
	}

	req := normalizeVercelRequest(r)
	apiHandler.ServeHTTP(w, req)
}

func buildHandler() (http.Handler, error) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	dataFile := strings.TrimSpace(os.Getenv("DATA_FILE"))
	cacheTTL := time.Duration(getEnvInt("CACHE_TTL_MINUTES", 15)) * time.Minute
	refreshCooldown := time.Duration(getEnvInt("REFRESH_COOLDOWN_SECONDS", 60)) * time.Second
	corsOrigins := resolveVercelCORSOrigins()
	rateLimitRPM := getEnvInt("RATE_LIMIT_RPM", 120)
	rateLimitBurst := getEnvInt("RATE_LIMIT_BURST", 30)
	webhookSecret := strings.TrimSpace(os.Getenv("WEBHOOK_SECRET"))
	track17Secret := strings.TrimSpace(os.Getenv("TRACK17_WEBHOOK_SECRET"))
	tmSecret := strings.TrimSpace(os.Getenv("TRACKINGMORE_WEBHOOK_SECRET"))
	adminToken := strings.TrimSpace(os.Getenv("ADMIN_TOKEN"))

	trackingStore, err := store.NewTrackingStore(dataFile, cacheTTL, refreshCooldown)
	if err != nil {
		return nil, err
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
	return srv.Handler(), nil
}

func resolveVercelCORSOrigins() []string {
	if configured := strings.TrimSpace(os.Getenv("CORS_ALLOWED_ORIGINS")); configured != "" {
		return splitCSV(configured)
	}
	origins := []string{"https://rastreio.alexandrejunior.com.br"}
	for _, envKey := range []string{"VERCEL_PROJECT_PRODUCTION_URL", "VERCEL_URL"} {
		if host := strings.TrimSpace(os.Getenv(envKey)); host != "" {
			if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
				host = "https://" + host
			}
			origins = append(origins, strings.TrimRight(host, "/"))
		}
	}
	return origins
}

func normalizeVercelRequest(r *http.Request) *http.Request {
	req := r.Clone(r.Context())
	q := req.URL.Query()

	if rewrittenPath := strings.TrimSpace(q.Get("__path")); rewrittenPath != "" {
		if !strings.HasPrefix(rewrittenPath, "/") {
			rewrittenPath = "/" + rewrittenPath
		}
		cleaned := path.Clean(rewrittenPath)
		q.Del("__path")
		req.URL.RawQuery = q.Encode()
		if strings.HasPrefix(cleaned, "/api/v1/") {
			req.URL.Path = cleaned
			req.URL.RawPath = ""
		} else {
			req.URL.Path = "/api/v1/__invalid__"
			req.URL.RawPath = ""
		}
		return req
	}

	switch req.URL.Path {
	case "/api", "/api/", "/api/index", "/api/index.go":
		req.URL.Path = "/api/v1/health"
	}
	return req
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
