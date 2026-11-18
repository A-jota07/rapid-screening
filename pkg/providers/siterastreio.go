package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	carrierpkg "rapid-screening/pkg/carrier"
	"rapid-screening/pkg/domain"
)

const (
	defaultSiteRastreioBaseURL = "https://seurastreio.com.br/api/public/rastreio"
	defaultSiteRastreioCredits = 1000
	defaultSiteRastreioTimeout = 6 * time.Second
	defaultSiteRastreioCircuit = 5 * time.Minute
)

// SiteRastreioConfig configura o provedor Site Rastreio / Seu Rastreio (cota gratuita mensal de 1.000 req/mês).
type SiteRastreioConfig struct {
	APIKey          string
	MonthlyCredits  int
	BaseURL         string
	HTTPClient      *http.Client
	CircuitDuration time.Duration
}

// SiteRastreioProvider integra com a API REST pública do ecossistema Site Rastreio / Seu Rastreio
// usando autenticação Bearer Token ("Authorization: Bearer sr_live_...").
type SiteRastreioProvider struct {
	state           *providerState
	apiKey          string
	baseURL         string
	client          *http.Client
	circuitDuration time.Duration
}

// NewSiteRastreioProviderFromEnv instancia o provedor lendo SITERASTREIO_API_KEY (ou SEURASTREIO_API_KEY)
// e SITERASTREIO_MONTHLY_CREDITS (default 1000).
func NewSiteRastreioProviderFromEnv() *SiteRastreioProvider {
	apiKey := strings.TrimSpace(os.Getenv("SITERASTREIO_API_KEY"))
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("SEURASTREIO_API_KEY"))
	}
	credits := parseEnvInt("SITERASTREIO_MONTHLY_CREDITS", defaultSiteRastreioCredits)
	baseURL := strings.TrimSpace(os.Getenv("SITERASTREIO_BASE_URL"))
	return NewSiteRastreioProvider(SiteRastreioConfig{
		APIKey:         apiKey,
		MonthlyCredits: credits,
		BaseURL:        baseURL,
	})
}

// NewSiteRastreioProvider cria um SiteRastreioProvider com configuração explícita.
func NewSiteRastreioProvider(cfg SiteRastreioConfig) *SiteRastreioProvider {
	apiKey := strings.TrimSpace(cfg.APIKey)
	credits := cfg.MonthlyCredits
	if credits < 0 {
		credits = defaultSiteRastreioCredits
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultSiteRastreioBaseURL
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultSiteRastreioTimeout}
	}
	circuitDur := cfg.CircuitDuration
	if circuitDur <= 0 {
		circuitDur = defaultSiteRastreioCircuit
	}

	return &SiteRastreioProvider{
		state:           newProviderState("siterastreio", "api_key", true, apiKey != "", credits),
		apiKey:          apiKey,
		baseURL:         baseURL,
		client:          client,
		circuitDuration: circuitDur,
	}
}

func (p *SiteRastreioProvider) Name() string {
	return "siterastreio"
}

func (p *SiteRastreioProvider) Status() domain.ProviderHealth {
	return p.state.health()
}

func (p *SiteRastreioProvider) Track(ctx context.Context, code string, carrierInfo domain.CarrierInfo) (*domain.TrackingResult, error) {
	if err := p.state.tryReserve(); err != nil {
		return nil, err
	}

	cleanCode := carrierpkg.SanitizeCode(code)
	if cleanCode == "" {
		p.state.refundCredit()
		return nil, fmt.Errorf("%w: empty tracking code", ErrNotFound)
	}

	start := p.state.now()
	endpoint := fmt.Sprintf("%s/%s", p.baseURL, url.PathEscape(cleanCode))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		p.state.refundCredit()
		p.state.recordFailure(time.Since(start), err, 0)
		return nil, fmt.Errorf("%w: create request: %v", ErrUnavailable, err)
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		p.state.refundCredit()
		wrapped := fmt.Errorf("%w: siterastreio network error: %v", ErrUnavailable, err)
		p.state.recordFailure(time.Since(start), wrapped, 0)
		return nil, wrapped
	}
	defer resp.Body.Close()

	rawBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		p.state.refundCredit()
		wrapped := fmt.Errorf("%w: siterastreio read body: %v", ErrUnavailable, err)
		p.state.recordFailure(time.Since(start), wrapped, 0)
		return nil, wrapped
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		p.state.refundCredit()
		authErr := fmt.Errorf("%w: siterastreio unauthorized HTTP %d", ErrUnavailable, resp.StatusCode)
		p.state.recordFailure(time.Since(start), authErr, p.circuitDuration)
		return nil, authErr
	}

	if resp.StatusCode == http.StatusPaymentRequired {
		p.state.exhaustCredits()
		quotaErr := fmt.Errorf("%w: siterastreio HTTP 402 subscription/quota required", ErrNoCredits)
		p.state.recordFailure(time.Since(start), quotaErr, p.circuitDuration)
		return nil, quotaErr
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		bodyStr := string(rawBody)
		lower := strings.ToLower(bodyStr)
		if strings.Contains(lower, "mensal") || strings.Contains(lower, "monthly") || strings.Contains(lower, "quota") || strings.Contains(lower, "cota") {
			p.state.exhaustCredits()
			quotaErr := fmt.Errorf("%w: siterastreio HTTP 429 monthly quota exhausted", ErrNoCredits)
			p.state.recordFailure(time.Since(start), quotaErr, p.circuitDuration)
			return nil, quotaErr
		}
		p.state.refundCredit()
		rateErr := fmt.Errorf("%w: siterastreio HTTP 429 rate limit", ErrUnavailable)
		p.state.recordFailure(time.Since(start), rateErr, p.circuitDuration)
		return nil, rateErr
	}

	if resp.StatusCode == http.StatusNotFound {
		p.state.refundCredit()
		notFoundErr := fmt.Errorf("%w: siterastreio HTTP 404 tracking not found", ErrNotFound)
		p.state.recordFailure(time.Since(start), notFoundErr, 0)
		return nil, notFoundErr
	}

	if resp.StatusCode >= 500 {
		p.state.refundCredit()
		srvErr := fmt.Errorf("%w: siterastreio HTTP %d", ErrUnavailable, resp.StatusCode)
		p.state.recordFailure(time.Since(start), srvErr, p.circuitDuration)
		return nil, srvErr
	}

	if resp.StatusCode != http.StatusOK {
		p.state.refundCredit()
		unexpectedErr := fmt.Errorf("%w: siterastreio unexpected HTTP %d: %s", ErrUnavailable, resp.StatusCode, strings.TrimSpace(string(rawBody)))
		p.state.recordFailure(time.Since(start), unexpectedErr, 0)
		return nil, unexpectedErr
	}

	var parsed siteRastreioResponse
	if err := json.Unmarshal(rawBody, &parsed); err != nil {
		p.state.refundCredit()
		parseErr := fmt.Errorf("%w: siterastreio invalid JSON: %v", ErrUnavailable, err)
		p.state.recordFailure(time.Since(start), parseErr, 0)
		return nil, parseErr
	}

	rawEvents := collectSiteRastreioEvents(parsed)
	if (parsed.Success != nil && !*parsed.Success) || len(rawEvents) == 0 {
		msg := strings.TrimSpace(parsed.Message)
		if msg == "" {
			msg = strings.TrimSpace(parsed.Error)
		}
		if isQuotaOrRateError(200, msg) {
			p.state.exhaustCredits()
			quotaErr := fmt.Errorf("%w: siterastreio quota message: %s", ErrNoCredits, msg)
			p.state.recordFailure(time.Since(start), quotaErr, p.circuitDuration)
			return nil, quotaErr
		}
		p.state.refundCredit()
		notFoundErr := fmt.Errorf("%w: siterastreio no events (%s)", ErrNotFound, msg)
		p.state.recordFailure(time.Since(start), notFoundErr, 0)
		return nil, notFoundErr
	}

	events := make([]domain.TrackingEvent, 0, len(rawEvents))
	for _, ev := range rawEvents {
		events = append(events, mapSiteRastreioEvent(ev))
	}

	result := &domain.TrackingResult{
		EstimatedDelivery: normalizeSiteRastreioEstimatedDate(parsed.PrevisaoEntrega),
		Events:            events,
	}

	finalizeTrackingResult(result, cleanCode, carrierInfo, p.Name(), p.state.now())
	p.state.recordSuccess(time.Since(start))
	return result, nil
}

func collectSiteRastreioEvents(parsed siteRastreioResponse) []siteRastreioEvent {
	if len(parsed.Historico) > 0 {
		return parsed.Historico
	}
	if len(parsed.Eventos) > 0 {
		return parsed.Eventos
	}
	if parsed.EventoMaisRecente != nil &&
		(strings.TrimSpace(parsed.EventoMaisRecente.Descricao) != "" ||
			strings.TrimSpace(parsed.EventoMaisRecente.Status) != "" ||
			strings.TrimSpace(parsed.EventoMaisRecente.Detalhe) != "") {
		return []siteRastreioEvent{*parsed.EventoMaisRecente}
	}
	return nil
}

func mapSiteRastreioEvent(ev siteRastreioEvent) domain.TrackingEvent {
	rawStatus := strings.TrimSpace(ev.Descricao)
	if rawStatus == "" {
		rawStatus = strings.TrimSpace(ev.Status)
	}
	if rawStatus == "" {
		rawStatus = strings.TrimSpace(ev.Detalhe)
	}

	combined := strings.TrimSpace(rawStatus + " " + ev.Detalhe)
	stdStatus, stdLabel, _ := carrierpkg.NormalizeStatus(combined)

	// Refina códigos SRO dos Correios quando o texto for genérico.
	if stdStatus == domain.StatusInTransit {
		switch strings.ToUpper(strings.TrimSpace(ev.Codigo)) {
		case "LDI":
			stdStatus = domain.StatusAtCDD
			stdLabel = domain.StatusLabel(domain.StatusAtCDD)
		case "OEC":
			stdStatus = domain.StatusOutForDelivery
			stdLabel = domain.StatusLabel(domain.StatusOutForDelivery)
		case "PO":
			stdStatus = domain.StatusPosted
			stdLabel = domain.StatusLabel(domain.StatusPosted)
		}
	}

	descParts := make([]string, 0, 3)
	if d := strings.TrimSpace(ev.Descricao); d != "" {
		descParts = append(descParts, d)
	}
	if det := strings.TrimSpace(ev.Detalhe); det != "" && !strings.EqualFold(det, ev.Descricao) {
		descParts = append(descParts, det)
	}
	if dest := strings.TrimSpace(ev.Destino); dest != "" {
		descParts = append(descParts, "Destino: "+dest)
	}

	loc := strings.TrimSpace(ev.Local)
	if loc == "" {
		loc = "Brasil"
	}

	return domain.TrackingEvent{
		Timestamp:      parseTimestamp(ev.Data),
		RawStatus:      rawStatus,
		StandardStatus: stdStatus,
		StandardLabel:  stdLabel,
		Description:    strings.Join(descParts, " — "),
		Location:       loc,
	}
}

func normalizeSiteRastreioEstimatedDate(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	t := parseTimestamp(trimmed)
	if !t.IsZero() && len(trimmed) >= 10 {
		return t.Format("2006-01-02")
	}
	return trimmed
}

type siteRastreioEvent struct {
	Codigo    string `json:"codigo"`
	Descricao string `json:"descricao"`
	Detalhe   string `json:"detalhe"`
	Data      string `json:"data"`
	Local     string `json:"local"`
	Destino   string `json:"destino"`
	Status    string `json:"status"`
}

type siteRastreioResponse struct {
	Codigo            string              `json:"codigo"`
	Status            string              `json:"status"`
	Success           *bool               `json:"success"`
	Message           string              `json:"message"`
	Error             string              `json:"error"`
	PrevisaoEntrega   string              `json:"previsaoEntrega"`
	EventoMaisRecente *siteRastreioEvent  `json:"eventoMaisRecente"`
	Historico         []siteRastreioEvent `json:"historico"`
	Eventos           []siteRastreioEvent `json:"eventos"`
}
