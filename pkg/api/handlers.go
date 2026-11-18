package api

import (
	"context"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"rapid-screening/pkg/carrier"
	"rapid-screening/pkg/domain"
	"rapid-screening/pkg/store"
)

// TrackerProvider abstrai a cadeia de provedores de rastreamento (*providers.ProviderChain).
type TrackerProvider interface {
	Track(ctx context.Context, code string, carrierInfo domain.CarrierInfo) (*domain.TrackingResult, error)
	Statuses() []domain.ProviderHealth
}

// ServerConfig agrupa as opções de inicialização da camada HTTP REST.
type ServerConfig struct {
	AllowedOrigins []string
	RateLimitRPM   int
	RateLimitBurst int
	WebhookSecret  string
	Logger         *slog.Logger
}

// Server expõe os endpoints RESTful e webhooks do Rapid Screening.
type Server struct {
	store         *store.TrackingStore
	enricher      domain.GeoEnricher
	tracker       TrackerProvider
	webhookSecret string
	logger        *slog.Logger
	rateLimiter   *RateLimiter
	corsOrigins   []string
}

// NewServer cria uma nova instância do servidor HTTP com store, geo-enriquecedor e cadeia de provedores.
func NewServer(st *store.TrackingStore, enricher domain.GeoEnricher, tracker TrackerProvider, cfg ServerConfig) *Server {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		store:         st,
		enricher:      enricher,
		tracker:       tracker,
		webhookSecret: strings.TrimSpace(cfg.WebhookSecret),
		logger:        logger,
		rateLimiter:   NewRateLimiter(cfg.RateLimitRPM, cfg.RateLimitBurst),
		corsOrigins:   cfg.AllowedOrigins,
	}
}

// Handler constrói o roteador Go 1.22+ (net/http) envolvido pelos middlewares de produção.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/carriers", s.handleListCarriers)
	mux.HandleFunc("GET /api/v1/carriers/detect", s.handleDetectCarrier)
	mux.HandleFunc("GET /api/v1/track/{code}", s.handleTrack)
	mux.HandleFunc("GET /api/v1/trackings/recent", s.handleRecentTrackings)
	mux.HandleFunc("POST /api/v1/webhooks/17track", s.handleWebhook17Track)
	mux.HandleFunc("POST /api/v1/webhooks/trackingmore", s.handleWebhookTrackingMore)

	var handler http.Handler = mux
	handler = s.rateLimiter.Middleware(handler)
	handler = CORSMiddleware(s.corsOrigins)(handler)
	handler = LoggingMiddleware(s.logger)(handler)
	handler = SecurityHeadersMiddleware(handler)
	handler = RecoverMiddleware(s.logger)(handler)
	return handler
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	statuses := []domain.ProviderHealth{}
	if s.tracker != nil {
		if ps := s.tracker.Statuses(); ps != nil {
			statuses = ps
		}
	}

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"service":   "rapid-screening-api",
		"timestamp": time.Now().UTC(),
		"cache":     s.store.Stats(),
		"providers": statuses,
	})
}

func (s *Server) handleListCarriers(w http.ResponseWriter, r *http.Request) {
	carriers := carrier.ListSupportedCarriers()
	if carriers == nil {
		carriers = []domain.CarrierInfo{}
	}
	w.Header().Set("Cache-Control", "public, s-maxage=3600, stale-while-revalidate=86400")
	writeJSON(w, http.StatusOK, map[string]any{
		"carriers": carriers,
	})
}

func (s *Server) handleDetectCarrier(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("code")
	code := carrier.SanitizeCode(raw)
	if len(code) < 3 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":   "invalid_tracking_code",
			"message": "Informe o parâmetro 'code' com um código de rastreio válido.",
		})
		return
	}

	detected := carrier.DetectCarrier(code)
	writeJSON(w, http.StatusOK, map[string]any{
		"code":    code,
		"carrier": detected,
	})
}

func (s *Server) handleTrack(w http.ResponseWriter, r *http.Request) {
	code := carrier.SanitizeCode(r.PathValue("code"))
	if len(code) < 5 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":   "invalid_tracking_code",
			"message": "Código de rastreio inválido. Verifique o formato e tente novamente.",
		})
		return
	}

	refresh := parseBoolParam(r.URL.Query().Get("refresh"))

	// 1. Consulta L1 Cache sem gastar créditos quando não há refresh explícito.
	if !refresh {
		if cached, ok := s.store.Get(code, false); ok {
			s.serveCached(w, cached, "HIT", 0, false)
			return
		}
	}

	// 2. Cooldown anti-abuso: protege créditos quando refresh é solicitado em janela curta ou após expiração recente.
	if canRefresh, retryAfter := s.store.CanRefresh(code); !canRefresh {
		secs := int(math.Ceil(retryAfter.Seconds()))
		if secs < 1 {
			secs = 1
		}
		if cached, ok := s.store.Get(code, true); ok {
			s.serveCached(w, cached, "COOLDOWN-HIT", secs, refresh)
			return
		}
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error":             "refresh_cooldown_active",
			"message":           "Atualização em cooldown anti-abuso. Tente novamente em instantes.",
			"retryAfterSeconds": secs,
		})
		return
	}

	// 3. Cache miss ou refresh autorizado: detectar transportadora e acionar cadeia de provedores.
	carrierInfo := carrier.DetectCarrier(code)
	if s.tracker == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "provider_chain_unavailable",
			"message": "Nenhum provedor de rastreamento configurado.",
		})
		return
	}

	result, err := s.tracker.Track(r.Context(), code, carrierInfo)
	if err != nil || result == nil {
		// Degradação graciosa: se houver cache stale no disco/memória, retorna preservando disponibilidade.
		if stale, ok := s.store.Get(code, true); ok {
			s.serveCached(w, stale, "STALE-FALLBACK", 0, refresh)
			return
		}
		msg := "Não foi possível obter informações de rastreio no momento."
		if err != nil {
			msg = err.Error()
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error":   "tracking_provider_error",
			"message": msg,
		})
		return
	}

	result.TrackingCode = code
	result.Cached = false
	if (result.Carrier.Code == "" || result.Carrier.Code == "unknown" || result.Carrier.Code == "auto") && carrierInfo.Code != "" {
		result.Carrier = carrierInfo
	}
	if result.CurrentStatusLabel == "" {
		result.CurrentStatusLabel = domain.StatusLabel(result.CurrentStatus)
	}
	if s.enricher != nil {
		s.enricher.Enrich(result)
	}
	if err := s.store.Save(result); err != nil {
		s.logger.Warn("failed_to_persist_tracking", "code", code, "error", err)
	}

	w.Header().Set("X-Cache-Status", "MISS")
	setTrackCacheControl(w, refresh)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) serveCached(w http.ResponseWriter, cached *domain.TrackingResult, cacheStatus string, cooldownSecs int, noStore bool) {
	cached.Cached = true
	cached.ProviderUsed = "cache"
	if s.enricher != nil && len(cached.RoutePoints) == 0 {
		s.enricher.Enrich(cached)
	}
	w.Header().Set("X-Cache-Status", cacheStatus)
	if cooldownSecs > 0 {
		w.Header().Set("X-Cache-Cooldown-Remaining", strconv.Itoa(cooldownSecs))
	}
	setTrackCacheControl(w, noStore)
	writeJSON(w, http.StatusOK, cached)
}

func setTrackCacheControl(w http.ResponseWriter, noStore bool) {
	if noStore {
		w.Header().Set("Cache-Control", "no-store")
		return
	}
	// Cache na CDN Edge da Vercel por 5 min + revalidação em background por 10 min para zerar gasto de créditos.
	w.Header().Set("Cache-Control", "public, s-maxage=300, stale-while-revalidate=600")
}

func (s *Server) handleRecentTrackings(w http.ResponseWriter, r *http.Request) {
	limit := 10
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			if parsed > 50 {
				parsed = 50
			}
			limit = parsed
		}
	}

	items := s.store.Recent(limit)
	if items == nil {
		items = []domain.RecentTrackingSummary{}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
	})
}

func parseBoolParam(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "sim":
		return true
	default:
		return false
	}
}
