package providers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	carrierpkg "rapid-screening/internal/carrier"
	"rapid-screening/internal/domain"
)

var (
	// ErrNoCredits indica que o provedor atingiu o limite de créditos mensais configurado.
	ErrNoCredits = errors.New("provider has no remaining credits")
	// ErrNotConfigured indica que o provedor não possui credenciais/chaves configuradas.
	ErrNotConfigured = errors.New("provider is not configured")
	// ErrUnavailable indica falha temporária de rede, HTTP 5xx ou provedor desabilitado.
	ErrUnavailable = errors.New("provider is temporarily unavailable")
	// ErrCircuitOpen indica que o circuit-breaker do provedor está aberto após falhas ou 429.
	ErrCircuitOpen = errors.New("provider circuit breaker is open")
	// ErrUnsupportedCarrier indica que o provedor não atende a transportadora solicitada.
	ErrUnsupportedCarrier = errors.New("carrier not supported by provider")
	// ErrNotFound indica que o objeto ainda não possui eventos disponíveis no provedor.
	ErrNotFound = errors.New("tracking information not found")
)

// Provider define o contrato comum para qualquer fonte de dados de rastreamento.
type Provider interface {
	Name() string
	Track(ctx context.Context, code string, carrier domain.CarrierInfo) (*domain.TrackingResult, error)
	Status() domain.ProviderHealth
}

// providerState encapsula de forma thread-safe o controle de créditos,
// métricas de uso/latência e circuit-breaker de um provedor.
type providerState struct {
	mu               sync.RWMutex
	name             string
	mode             string
	enabled          bool
	configured       bool
	creditsRemaining int // -1 significa ilimitado/gratuito
	requestsUsed     int64
	failures         int64
	lastError        string
	lastLatencyMs    int64
	circuitUntil     *time.Time
	nowFn            func() time.Time
}

func newProviderState(name, mode string, enabled, configured bool, credits int) *providerState {
	return &providerState{
		name:             name,
		mode:             mode,
		enabled:          enabled,
		configured:       configured,
		creditsRemaining: credits,
		nowFn:            time.Now,
	}
}

func (s *providerState) now() time.Time {
	if s.nowFn != nil {
		return s.nowFn()
	}
	return time.Now()
}

// tryReserve verifica disponibilidade, circuit-breaker e reserva 1 crédito (quando finito).
func (s *providerState) tryReserve() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.configured {
		return ErrNotConfigured
	}
	if !s.enabled {
		return ErrUnavailable
	}

	now := s.now()
	if s.circuitUntil != nil && now.Before(*s.circuitUntil) {
		return ErrCircuitOpen
	}
	if s.circuitUntil != nil && !now.Before(*s.circuitUntil) {
		s.circuitUntil = nil
	}

	if s.creditsRemaining == 0 {
		return ErrNoCredits
	}
	if s.creditsRemaining > 0 {
		s.creditsRemaining--
	}
	s.requestsUsed++
	return nil
}

// checkReady verifica se o provedor gratuito (sem consumo de créditos) pode ser acionado.
func (s *providerState) checkReady() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.configured {
		return ErrNotConfigured
	}
	if !s.enabled {
		return ErrUnavailable
	}

	now := s.now()
	if s.circuitUntil != nil && now.Before(*s.circuitUntil) {
		return ErrCircuitOpen
	}
	if s.circuitUntil != nil && !now.Before(*s.circuitUntil) {
		s.circuitUntil = nil
	}
	s.requestsUsed++
	return nil
}

func (s *providerState) refundCredit() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.creditsRemaining >= 0 {
		s.creditsRemaining++
	}
}

func (s *providerState) exhaustCredits() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.creditsRemaining >= 0 {
		s.creditsRemaining = 0
	}
}

func (s *providerState) recordSuccess(latency time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastLatencyMs = latency.Milliseconds()
	s.lastError = ""
	s.circuitUntil = nil
}

func (s *providerState) recordFailure(latency time.Duration, err error, openCircuitFor time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures++
	s.lastLatencyMs = latency.Milliseconds()
	if err != nil {
		s.lastError = err.Error()
	}
	if openCircuitFor > 0 {
		until := s.now().Add(openCircuitFor)
		s.circuitUntil = &until
	}
}

func (s *providerState) health() domain.ProviderHealth {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := s.now()
	circuitOpen := s.circuitUntil != nil && now.Before(*s.circuitUntil)
	var circuitUntilCopy *time.Time
	if circuitOpen && s.circuitUntil != nil {
		t := *s.circuitUntil
		circuitUntilCopy = &t
	}

	hasCredits := s.creditsRemaining == -1 || s.creditsRemaining > 0
	available := s.enabled && s.configured && !circuitOpen && hasCredits

	return domain.ProviderHealth{
		Name:             s.name,
		Enabled:          s.enabled,
		Configured:       s.configured,
		Available:        available,
		CircuitOpen:      circuitOpen,
		CircuitUntil:     circuitUntilCopy,
		CreditsRemaining: s.creditsRemaining,
		RequestsUsed:     s.requestsUsed,
		Failures:         s.failures,
		LastError:        s.lastError,
		LastLatencyMs:    s.lastLatencyMs,
		Mode:             s.mode,
	}
}

func parseEnvInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	val, err := strconv.Atoi(raw)
	if err != nil || val < 0 {
		return fallback
	}
	return val
}

func isQuotaOrRateError(httpStatus int, msg string) bool {
	if httpStatus == 429 || httpStatus == 402 {
		return true
	}
	lower := strings.ToLower(msg)
	for _, kw := range []string{
		"quota",
		"credit",
		"rate limit",
		"ratelimit",
		"too many",
		"exceeded",
		"insufficient",
		"balance",
		"limit reached",
	} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func parseTimestamp(candidates ...string) time.Time {
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05-0700",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"02/01/2006 15:04:05",
		"02/01/2006 15:04",
		"2006-01-02",
	}

	for _, c := range candidates {
		trimmed := strings.TrimSpace(c)
		if trimmed == "" {
			continue
		}
		for _, layout := range layouts {
			if t, err := time.Parse(layout, trimmed); err == nil {
				return t.UTC()
			}
		}
	}
	return time.Now().UTC()
}

func sortEventsDesc(events []domain.TrackingEvent) {
	sort.SliceStable(events, func(i, j int) bool {
		return events[i].Timestamp.After(events[j].Timestamp)
	})
}

func finalizeTrackingResult(res *domain.TrackingResult, code string, carrierInfo domain.CarrierInfo, providerName string, now time.Time) {
	res.TrackingCode = code
	res.Carrier = carrierInfo
	res.ProviderUsed = providerName
	res.LastUpdatedAt = now.UTC()
	if res.CacheExpiresAt.IsZero() {
		res.CacheExpiresAt = now.UTC().Add(5 * time.Minute)
	}

	sortEventsDesc(res.Events)
	if len(res.Events) > 0 {
		latest := res.Events[0]
		if res.CurrentStatus == "" {
			res.CurrentStatus = latest.StandardStatus
			res.CurrentStatusLabel = latest.StandardLabel
		}
		if res.ProgressStep == 0 {
			_, _, step := carrierpkg.NormalizeStatus(latest.RawStatus)
			res.ProgressStep = step
		}
	}
	if res.CurrentStatus == "" {
		res.CurrentStatus = domain.StatusInTransit
		res.CurrentStatusLabel = domain.StatusLabel(domain.StatusInTransit)
		res.ProgressStep = 2
	}
	if res.CurrentStatusLabel == "" {
		res.CurrentStatusLabel = domain.StatusLabel(res.CurrentStatus)
	}
	if res.RoutePoints == nil {
		res.RoutePoints = []domain.GeoPoint{}
	}
	if res.Events == nil {
		res.Events = []domain.TrackingEvent{}
	}
	if res.EstimatedDelivery == "" {
		switch res.CurrentStatus {
		case domain.StatusDelivered:
			res.EstimatedDelivery = fmt.Sprintf("Entregue em %s", now.UTC().Format("02/01/2006"))
		default:
			res.EstimatedDelivery = now.UTC().AddDate(0, 0, 2).Format("2006-01-02")
		}
	}
}
