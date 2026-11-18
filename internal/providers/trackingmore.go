package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	carrierpkg "rapid-screening/internal/carrier"
	"rapid-screening/internal/domain"
)

const (
	defaultTrackingMoreBaseURL = "https://api.trackingmore.com/v4"
	defaultTrackingMoreCredits = 50
	defaultTrackingMoreTimeout = 8 * time.Second
	defaultTrackingMoreCircuit = 5 * time.Minute
)

// TrackingMoreConfig configura o provedor TrackingMore v4.
type TrackingMoreConfig struct {
	APIKey          string
	MonthlyCredits  int
	BaseURL         string
	HTTPClient      *http.Client
	CircuitDuration time.Duration
}

// TrackingMoreProvider integra com a API real TrackingMore v4 usando header "Tracking-Api-Key".
type TrackingMoreProvider struct {
	state           *providerState
	apiKey          string
	baseURL         string
	client          *http.Client
	circuitDuration time.Duration
}

// NewTrackingMoreProviderFromEnv instancia o provedor lendo TRACKINGMORE_API_KEY e TRACKINGMORE_MONTHLY_CREDITS (default 50).
func NewTrackingMoreProviderFromEnv() *TrackingMoreProvider {
	apiKey := strings.TrimSpace(os.Getenv("TRACKINGMORE_API_KEY"))
	credits := parseEnvInt("TRACKINGMORE_MONTHLY_CREDITS", defaultTrackingMoreCredits)
	baseURL := strings.TrimSpace(os.Getenv("TRACKINGMORE_BASE_URL"))
	return NewTrackingMoreProvider(TrackingMoreConfig{
		APIKey:         apiKey,
		MonthlyCredits: credits,
		BaseURL:        baseURL,
	})
}

// NewTrackingMoreProvider cria um TrackingMoreProvider com configuração explícita.
func NewTrackingMoreProvider(cfg TrackingMoreConfig) *TrackingMoreProvider {
	apiKey := strings.TrimSpace(cfg.APIKey)
	credits := cfg.MonthlyCredits
	if credits < 0 {
		credits = defaultTrackingMoreCredits
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultTrackingMoreBaseURL
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultTrackingMoreTimeout}
	}
	circuitDur := cfg.CircuitDuration
	if circuitDur <= 0 {
		circuitDur = defaultTrackingMoreCircuit
	}

	configured := apiKey != ""
	return &TrackingMoreProvider{
		state:           newProviderState("trackingmore", "api_key", true, configured, credits),
		apiKey:          apiKey,
		baseURL:         baseURL,
		client:          client,
		circuitDuration: circuitDur,
	}
}

// Name retorna "trackingmore".
func (p *TrackingMoreProvider) Name() string {
	return "trackingmore"
}

// Status retorna saúde, créditos restantes e estado do circuit-breaker da TrackingMore.
func (p *TrackingMoreProvider) Status() domain.ProviderHealth {
	return p.state.health()
}

// Track consulta a API TrackingMore v4 (/trackings/get e /trackings/create quando necessário).
func (p *TrackingMoreProvider) Track(ctx context.Context, code string, carrierInfo domain.CarrierInfo) (*domain.TrackingResult, error) {
	cleanCode := carrierpkg.SanitizeCode(code)
	if cleanCode == "" {
		return nil, fmt.Errorf("%w: empty tracking code", ErrNotFound)
	}

	if err := p.state.tryReserve(); err != nil {
		return nil, err
	}

	start := time.Now()
	courierCode := ""
	if carrierInfo.Code != "" && carrierInfo.Code != "auto" {
		courierCode = carrierInfo.TrackingMoreCode
	}

	items, metaCode, err := p.getTracking(ctx, cleanCode, courierCode)
	if err != nil {
		p.handleCallFailure(start, err)
		return nil, err
	}

	// Se o rastreamento ainda não existe na conta TrackingMore, cria e obtém os eventos.
	if len(items) == 0 || metaCode == 4031 || metaCode == 4102 || metaCode == 404 {
		createdItem, createMetaCode, createErr := p.createTracking(ctx, cleanCode, courierCode)
		if createErr != nil {
			p.handleCallFailure(start, createErr)
			return nil, createErr
		}
		if createMetaCode == 4016 {
			// Já existia: refaz GET.
			items, _, err = p.getTracking(ctx, cleanCode, courierCode)
			if err != nil {
				p.handleCallFailure(start, err)
				return nil, err
			}
		} else if createdItem != nil {
			items = []trackingMoreItem{*createdItem}
		}
	}

	if len(items) == 0 {
		notFoundErr := fmt.Errorf("%w: trackingmore returned no items", ErrNotFound)
		p.state.refundCredit()
		p.state.recordFailure(time.Since(start), notFoundErr, 0)
		return nil, notFoundErr
	}

	result := mapTrackingMoreItem(items[0], cleanCode, carrierInfo, p.Name())
	p.state.recordSuccess(time.Since(start))
	return result, nil
}

func (p *TrackingMoreProvider) handleCallFailure(start time.Time, err error) {
	if errors.Is(err, ErrNoCredits) {
		p.state.exhaustCredits()
	} else {
		p.state.refundCredit()
	}
	var circuitDur time.Duration
	if isCircuitTriggeringError(err) || errors.Is(err, ErrNoCredits) {
		circuitDur = p.circuitDuration
	}
	p.state.recordFailure(time.Since(start), err, circuitDur)
}

func (p *TrackingMoreProvider) getTracking(ctx context.Context, code, courierCode string) ([]trackingMoreItem, int, error) {
	params := url.Values{}
	params.Set("tracking_numbers", code)
	if courierCode != "" {
		params.Set("courier_code", courierCode)
	}
	endpoint := fmt.Sprintf("%s/trackings/get?%s", p.baseURL, params.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: create request: %v", ErrUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Tracking-Api-Key", p.apiKey)

	respBody, httpStatus, err := p.doRequest(req)
	if err != nil {
		return nil, httpStatus, err
	}

	var parsed trackingMoreEnvelope
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, httpStatus, fmt.Errorf("%w: trackingmore invalid JSON: %v", ErrUnavailable, err)
	}

	if err := classifyTrackingMoreMeta(httpStatus, parsed.Meta, true); err != nil {
		return nil, parsed.Meta.Code, err
	}

	items, err := decodeTrackingMoreItems(parsed.Data)
	if err != nil {
		return nil, parsed.Meta.Code, fmt.Errorf("%w: decode trackingmore items: %v", ErrUnavailable, err)
	}
	return items, parsed.Meta.Code, nil
}

func (p *TrackingMoreProvider) createTracking(ctx context.Context, code, courierCode string) (*trackingMoreItem, int, error) {
	payload := struct {
		TrackingNumber string `json:"tracking_number"`
		CourierCode    string `json:"courier_code,omitempty"`
	}{
		TrackingNumber: code,
		CourierCode:    courierCode,
	}

	rawBody, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: marshal create payload: %v", ErrUnavailable, err)
	}

	endpoint := fmt.Sprintf("%s/trackings/create", p.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(rawBody))
	if err != nil {
		return nil, 0, fmt.Errorf("%w: create request: %v", ErrUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Tracking-Api-Key", p.apiKey)

	respBody, httpStatus, err := p.doRequest(req)
	if err != nil {
		return nil, httpStatus, err
	}

	var parsed trackingMoreEnvelope
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, httpStatus, fmt.Errorf("%w: trackingmore create invalid JSON: %v", ErrUnavailable, err)
	}

	if parsed.Meta.Code == 4016 {
		return nil, 4016, nil
	}

	if err := classifyTrackingMoreMeta(httpStatus, parsed.Meta, false); err != nil {
		return nil, parsed.Meta.Code, err
	}

	items, err := decodeTrackingMoreItems(parsed.Data)
	if err != nil {
		return nil, parsed.Meta.Code, fmt.Errorf("%w: decode create item: %v", ErrUnavailable, err)
	}
	if len(items) == 0 {
		return nil, parsed.Meta.Code, nil
	}
	return &items[0], parsed.Meta.Code, nil
}

func (p *TrackingMoreProvider) doRequest(req *http.Request) ([]byte, int, error) {
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: trackingmore network error: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("%w: trackingmore read body: %v", ErrUnavailable, err)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, resp.StatusCode, fmt.Errorf("%w: trackingmore HTTP 429 rate limit", ErrUnavailable)
	}
	if resp.StatusCode == http.StatusPaymentRequired {
		return nil, resp.StatusCode, fmt.Errorf("%w: trackingmore HTTP 402 quota exceeded", ErrNoCredits)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, resp.StatusCode, fmt.Errorf("%w: trackingmore unauthorized HTTP %d", ErrUnavailable, resp.StatusCode)
	}
	if resp.StatusCode >= 500 {
		return nil, resp.StatusCode, fmt.Errorf("%w: trackingmore HTTP %d", ErrUnavailable, resp.StatusCode)
	}

	return raw, resp.StatusCode, nil
}

func classifyTrackingMoreMeta(httpStatus int, meta trackingMoreMeta, allowNotFound bool) error {
	if meta.Code == 0 || meta.Code == 200 || meta.Code == 201 {
		return nil
	}
	if allowNotFound && (meta.Code == 4031 || meta.Code == 4102 || meta.Code == 404) {
		return nil
	}
	// Códigos TrackingMore v4 de cota/rate-limit.
	switch meta.Code {
	case 4017, 4018, 4021:
		return fmt.Errorf("%w: trackingmore quota exceeded (%d: %s)", ErrNoCredits, meta.Code, meta.Message)
	case 429, 4190, 4191:
		return fmt.Errorf("%w: trackingmore HTTP 429/rate limit (%d: %s)", ErrUnavailable, meta.Code, meta.Message)
	}
	if isQuotaOrRateError(httpStatus, meta.Message) {
		lower := strings.ToLower(meta.Message)
		if strings.Contains(lower, "rate limit") || strings.Contains(lower, "too many") {
			return fmt.Errorf("%w: trackingmore HTTP 429/rate limit (%d: %s)", ErrUnavailable, meta.Code, meta.Message)
		}
		return fmt.Errorf("%w: trackingmore quota error (%d: %s)", ErrNoCredits, meta.Code, meta.Message)
	}
	if meta.Code == 401 || meta.Code == 403 {
		return fmt.Errorf("%w: trackingmore unauthorized (%d: %s)", ErrUnavailable, meta.Code, meta.Message)
	}
	return fmt.Errorf("%w: trackingmore API error (%d: %s)", ErrUnavailable, meta.Code, meta.Message)
}

func decodeTrackingMoreItems(raw json.RawMessage) ([]trackingMoreItem, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	if strings.HasPrefix(trimmed, "[") {
		var items []trackingMoreItem
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, err
		}
		return items, nil
	}
	if strings.HasPrefix(trimmed, "{") {
		var item trackingMoreItem
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		return []trackingMoreItem{item}, nil
	}
	return nil, nil
}

func mapTrackingMoreItem(item trackingMoreItem, cleanCode string, carrierInfo domain.CarrierInfo, providerName string) *domain.TrackingResult {
	if carrierInfo.Code == "" || carrierInfo.Code == "auto" {
		if item.CourierCode != "" {
			carrierInfo.TrackingMoreCode = item.CourierCode
			carrierInfo.DetectedBy = "aggregator"
			carrierInfo.Confidence = 0.85
		}
	}

	rawCheckpoints := append([]trackingMoreCheckpoint{}, item.DestinationInfo.TrackInfo...)
	rawCheckpoints = append(rawCheckpoints, item.OriginInfo.TrackInfo...)

	seen := make(map[string]struct{}, len(rawCheckpoints))
	events := make([]domain.TrackingEvent, 0, len(rawCheckpoints))

	for _, cp := range rawCheckpoints {
		desc := strings.TrimSpace(cp.TrackingDetail)
		if desc == "" {
			desc = strings.TrimSpace(cp.CheckpointDeliveryStatus)
		}
		ts := parseTimestamp(cp.CheckpointDate)
		dedupKey := ts.Format(time.RFC3339) + "|" + desc
		if _, ok := seen[dedupKey]; ok {
			continue
		}
		seen[dedupKey] = struct{}{}

		stdStatus, stdLabel := normalizeEventWithStage(desc, cp.CheckpointDeliveryStatus)
		loc := buildTrackingMoreLocation(cp)
		var coord *domain.GeoPoint
		if cp.City != "" || cp.State != "" {
			coord = &domain.GeoPoint{
				City:         cp.City,
				State:        cp.State,
				FacilityType: "HUB",
				FacilityName: loc,
			}
		}

		events = append(events, domain.TrackingEvent{
			Timestamp:      ts,
			RawStatus:      desc,
			StandardStatus: stdStatus,
			StandardLabel:  stdLabel,
			Description:    desc,
			Location:       loc,
			Coordinates:    coord,
		})
	}

	result := &domain.TrackingResult{
		EstimatedDelivery: strings.TrimSpace(item.ScheduledDeliveryDate),
		Events:            events,
	}
	if len(events) == 0 && item.DeliveryStatus != "" {
		st, lbl, step := carrierpkg.NormalizeStatus(item.DeliveryStatus)
		result.CurrentStatus = st
		result.CurrentStatusLabel = lbl
		result.ProgressStep = step
	}

	finalizeTrackingResult(result, cleanCode, carrierInfo, providerName, time.Now())
	return result
}

func buildTrackingMoreLocation(cp trackingMoreCheckpoint) string {
	if strings.TrimSpace(cp.Location) != "" {
		return strings.TrimSpace(cp.Location)
	}
	if cp.City != "" && cp.State != "" {
		return cp.City + " - " + cp.State
	}
	if cp.City != "" {
		return cp.City
	}
	if cp.CountryISO2 != "" {
		return cp.CountryISO2
	}
	return "Em trânsito"
}

type trackingMoreMeta struct {
	Code    int    `json:"code"`
	Type    string `json:"type"`
	Message string `json:"message"`
}

type trackingMoreEnvelope struct {
	Meta trackingMoreMeta `json:"meta"`
	Data json.RawMessage  `json:"data"`
}

type trackingMoreCheckpoint struct {
	CheckpointDate           string `json:"checkpoint_date"`
	CheckpointDeliveryStatus string `json:"checkpoint_delivery_status"`
	TrackingDetail           string `json:"tracking_detail"`
	Location                 string `json:"location"`
	City                     string `json:"city"`
	State                    string `json:"state"`
	CountryISO2              string `json:"country_iso2"`
}

type trackingMoreInfoBlock struct {
	CourierCode string                   `json:"courier_code"`
	TrackInfo   []trackingMoreCheckpoint `json:"trackinfo"`
}

type trackingMoreItem struct {
	ID                    string                `json:"id"`
	TrackingNumber        string                `json:"tracking_number"`
	CourierCode           string                `json:"courier_code"`
	DeliveryStatus        string                `json:"delivery_status"`
	ScheduledDeliveryDate string                `json:"scheduled_delivery_date"`
	OriginInfo            trackingMoreInfoBlock `json:"origin_info"`
	DestinationInfo       trackingMoreInfoBlock `json:"destination_info"`
}
