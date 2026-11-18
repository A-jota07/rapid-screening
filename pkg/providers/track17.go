package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	carrierpkg "rapid-screening/pkg/carrier"
	"rapid-screening/pkg/domain"
)

const (
	defaultTrack17BaseURL = "https://api.17track.net/track/v2.2"
	defaultTrack17Credits = 100
	defaultTrack17Timeout = 8 * time.Second
	defaultTrack17Circuit = 5 * time.Minute
)

// Track17Config configura o provedor 17TRACK v2.2.
type Track17Config struct {
	APIKey          string
	MonthlyCredits  int
	BaseURL         string
	HTTPClient      *http.Client
	CircuitDuration time.Duration
}

// Track17Provider integra com a API real 17TRACK v2.2 usando header "17token".
type Track17Provider struct {
	state           *providerState
	apiKey          string
	baseURL         string
	client          *http.Client
	circuitDuration time.Duration
}

// NewTrack17ProviderFromEnv instancia o provedor lendo TRACK17_API_KEY e TRACK17_MONTHLY_CREDITS (default 100).
func NewTrack17ProviderFromEnv() *Track17Provider {
	apiKey := strings.TrimSpace(os.Getenv("TRACK17_API_KEY"))
	credits := parseEnvInt("TRACK17_MONTHLY_CREDITS", defaultTrack17Credits)
	baseURL := strings.TrimSpace(os.Getenv("TRACK17_BASE_URL"))
	return NewTrack17Provider(Track17Config{
		APIKey:         apiKey,
		MonthlyCredits: credits,
		BaseURL:        baseURL,
	})
}

// NewTrack17Provider cria um Track17Provider com configuração explícita.
func NewTrack17Provider(cfg Track17Config) *Track17Provider {
	apiKey := strings.TrimSpace(cfg.APIKey)
	credits := cfg.MonthlyCredits
	if credits < 0 {
		credits = defaultTrack17Credits
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultTrack17BaseURL
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultTrack17Timeout}
	}
	circuitDur := cfg.CircuitDuration
	if circuitDur <= 0 {
		circuitDur = defaultTrack17Circuit
	}

	configured := apiKey != ""
	return &Track17Provider{
		state:           newProviderState("17track", "api_key", true, configured, credits),
		apiKey:          apiKey,
		baseURL:         baseURL,
		client:          client,
		circuitDuration: circuitDur,
	}
}

// Name retorna "17track".
func (p *Track17Provider) Name() string {
	return "17track"
}

// Status retorna a saúde, créditos restantes e estado de circuit-breaker da 17TRACK.
func (p *Track17Provider) Status() domain.ProviderHealth {
	return p.state.health()
}

// Track consulta a API 17TRACK v2.2 (/gettrackinfo e /register quando necessário).
func (p *Track17Provider) Track(ctx context.Context, code string, carrierInfo domain.CarrierInfo) (*domain.TrackingResult, error) {
	cleanCode := carrierpkg.SanitizeCode(code)
	if cleanCode == "" {
		return nil, fmt.Errorf("%w: empty tracking code", ErrNotFound)
	}

	if err := p.state.tryReserve(); err != nil {
		return nil, err
	}

	start := time.Now()
	resp, err := p.postTrackRequest(ctx, "gettrackinfo", cleanCode, carrierInfo.Track17CarrierID)
	if err != nil {
		p.handleCallFailure(start, err)
		return nil, err
	}

	// Se o número ainda não foi registrado na 17TRACK, registra e consulta novamente.
	if len(resp.Data.Accepted) == 0 && len(resp.Data.Rejected) > 0 && is17TrackNotRegistered(resp.Data.Rejected[0].Error) {
		regResp, regErr := p.postTrackRequest(ctx, "register", cleanCode, carrierInfo.Track17CarrierID)
		if regErr != nil {
			p.handleCallFailure(start, regErr)
			return nil, regErr
		}
		if len(regResp.Data.Rejected) > 0 && len(regResp.Data.Accepted) == 0 {
			rej := regResp.Data.Rejected[0].Error
			if is17TrackQuotaCode(rej.Code) || isQuotaOrRateError(200, rej.Message) {
				quotaErr := fmt.Errorf("%w: 17track register quota exceeded (%d: %s)", ErrNoCredits, rej.Code, rej.Message)
				p.state.exhaustCredits()
				p.state.recordFailure(time.Since(start), quotaErr, p.circuitDuration)
				return nil, quotaErr
			}
			rejErr := fmt.Errorf("%w: 17track register rejected (%d: %s)", ErrUnavailable, rej.Code, rej.Message)
			p.state.refundCredit()
			p.state.recordFailure(time.Since(start), rejErr, 0)
			return nil, rejErr
		}

		resp, err = p.postTrackRequest(ctx, "gettrackinfo", cleanCode, carrierInfo.Track17CarrierID)
		if err != nil {
			p.handleCallFailure(start, err)
			return nil, err
		}
	}

	if len(resp.Data.Accepted) == 0 {
		if len(resp.Data.Rejected) > 0 {
			rej := resp.Data.Rejected[0].Error
			if is17TrackQuotaCode(rej.Code) || isQuotaOrRateError(200, rej.Message) {
				quotaErr := fmt.Errorf("%w: 17track quota error (%d: %s)", ErrNoCredits, rej.Code, rej.Message)
				p.state.exhaustCredits()
				p.state.recordFailure(time.Since(start), quotaErr, p.circuitDuration)
				return nil, quotaErr
			}
			rejErr := fmt.Errorf("%w: 17track rejected (%d: %s)", ErrNotFound, rej.Code, rej.Message)
			p.state.refundCredit()
			p.state.recordFailure(time.Since(start), rejErr, 0)
			return nil, rejErr
		}
		notFoundErr := fmt.Errorf("%w: 17track returned no tracking data", ErrNotFound)
		p.state.refundCredit()
		p.state.recordFailure(time.Since(start), notFoundErr, 0)
		return nil, notFoundErr
	}

	result := mapTrack17Accepted(resp.Data.Accepted[0], cleanCode, carrierInfo, p.Name())
	p.state.recordSuccess(time.Since(start))
	return result, nil
}

func (p *Track17Provider) handleCallFailure(start time.Time, err error) {
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

func isCircuitTriggeringError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "429") ||
		strings.Contains(msg, "quota") ||
		strings.Contains(msg, "credit") ||
		strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "unauthorized") ||
		strings.Contains(msg, "401") ||
		strings.Contains(msg, "403")
}

func (p *Track17Provider) postTrackRequest(ctx context.Context, action, code string, carrierID int) (*track17Response, error) {
	item := track17RequestItem{Number: code}
	if carrierID > 0 {
		item.Carrier = carrierID
	}
	bodyBytes, err := json.Marshal([]track17RequestItem{item})
	if err != nil {
		return nil, fmt.Errorf("%w: marshal request: %v", ErrUnavailable, err)
	}

	endpoint := fmt.Sprintf("%s/%s", p.baseURL, action)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("%w: create request: %v", ErrUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("17token", p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: 17track network error: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: 17track read body: %v", ErrUnavailable, err)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("%w: 17track HTTP 429 rate limit", ErrUnavailable)
	}
	if resp.StatusCode == http.StatusPaymentRequired {
		return nil, fmt.Errorf("%w: 17track HTTP 402 quota exceeded", ErrNoCredits)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("%w: 17track unauthorized HTTP %d", ErrUnavailable, resp.StatusCode)
	}
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("%w: 17track HTTP %d", ErrUnavailable, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: 17track unexpected HTTP %d: %s", ErrUnavailable, resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var parsed track17Response
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("%w: 17track invalid JSON: %v", ErrUnavailable, err)
	}

	if parsed.Code != 0 {
		if parsed.Code == -18010012 || parsed.Code == -18019904 || isQuotaOrRateError(0, parsed.Message) {
			if strings.Contains(strings.ToLower(parsed.Message), "rate limit") || strings.Contains(strings.ToLower(parsed.Message), "too many") {
				return nil, fmt.Errorf("%w: 17track rate limit (%d: %s)", ErrUnavailable, parsed.Code, parsed.Message)
			}
			return nil, fmt.Errorf("%w: 17track quota error (%d: %s)", ErrNoCredits, parsed.Code, parsed.Message)
		}
		if parsed.Code == -18010011 {
			return nil, fmt.Errorf("%w: 17track rate limit (%d: %s)", ErrUnavailable, parsed.Code, parsed.Message)
		}
		return nil, fmt.Errorf("%w: 17track API error (%d: %s)", ErrUnavailable, parsed.Code, parsed.Message)
	}

	return &parsed, nil
}

func is17TrackNotRegistered(e track17Error) bool {
	if e.Code == -18019902 {
		return true
	}
	lower := strings.ToLower(e.Message)
	return strings.Contains(lower, "not registered") || strings.Contains(lower, "register")
}

func is17TrackQuotaCode(code int) bool {
	// Códigos documentados da 17TRACK v2.2 para limite/cota/rate limit.
	return code == -18010011 || code == -18010012 || code == -18019904
}

func mapTrack17Accepted(item track17AcceptedItem, cleanCode string, carrierInfo domain.CarrierInfo, providerName string) *domain.TrackingResult {
	if carrierInfo.Code == "" || carrierInfo.Code == "auto" {
		if item.Carrier > 0 {
			carrierInfo.Track17CarrierID = item.Carrier
		}
		if len(item.TrackInfo.Tracking.Providers) > 0 && item.TrackInfo.Tracking.Providers[0].Provider.Name != "" {
			carrierInfo.Name = item.TrackInfo.Tracking.Providers[0].Provider.Name
			carrierInfo.DetectedBy = "aggregator"
			carrierInfo.Confidence = 0.85
		}
	}

	var events []domain.TrackingEvent
	for _, prov := range item.TrackInfo.Tracking.Providers {
		for _, ev := range prov.Events {
			raw := strings.TrimSpace(ev.Description)
			if raw == "" {
				raw = strings.TrimSpace(ev.Stage)
			}
			stdStatus, stdLabel := normalizeEventWithStage(raw, ev.Stage)
			loc := build17TrackLocation(ev.Location, ev.Address)
			var coord *domain.GeoPoint
			if ev.Address.City != "" || ev.Address.State != "" {
				coord = &domain.GeoPoint{
					City:         ev.Address.City,
					State:        ev.Address.State,
					FacilityType: "HUB",
					FacilityName: loc,
				}
			}
			events = append(events, domain.TrackingEvent{
				Timestamp:      parseTimestamp(ev.TimeISO, ev.TimeUTC),
				RawStatus:      raw,
				StandardStatus: stdStatus,
				StandardLabel:  stdLabel,
				Description:    raw,
				Location:       loc,
				Coordinates:    coord,
			})
		}
	}

	if len(events) == 0 && item.TrackInfo.LatestEvent.Description != "" {
		le := item.TrackInfo.LatestEvent
		stdStatus, stdLabel := normalizeEventWithStage(le.Description, item.TrackInfo.LatestStatus.Status)
		events = append(events, domain.TrackingEvent{
			Timestamp:      parseTimestamp(le.TimeISO, le.TimeUTC),
			RawStatus:      le.Description,
			StandardStatus: stdStatus,
			StandardLabel:  stdLabel,
			Description:    le.Description,
			Location:       le.Location,
		})
	}

	result := &domain.TrackingResult{
		Events: events,
	}
	if len(events) == 0 && item.TrackInfo.LatestStatus.Status != "" {
		st, lbl, step := carrierpkg.NormalizeStatus(item.TrackInfo.LatestStatus.Status)
		result.CurrentStatus = st
		result.CurrentStatusLabel = lbl
		result.ProgressStep = step
	}

	finalizeTrackingResult(result, cleanCode, carrierInfo, providerName, time.Now())
	return result
}

func build17TrackLocation(loc string, addr track17Address) string {
	loc = strings.TrimSpace(loc)
	if loc != "" {
		return loc
	}
	if addr.City != "" && addr.State != "" {
		return addr.City + " - " + addr.State
	}
	if addr.City != "" {
		return addr.City
	}
	if addr.Country != "" {
		return addr.Country
	}
	return "Em trânsito"
}

func normalizeEventWithStage(rawDesc, stage string) (domain.StandardStatus, string) {
	st, lbl, _ := carrierpkg.NormalizeStatus(rawDesc)
	if strings.TrimSpace(stage) != "" && st == domain.StatusInTransit {
		stageSt, stageLbl, _ := carrierpkg.NormalizeStatus(stage)
		if stageSt != domain.StatusInTransit {
			return stageSt, stageLbl
		}
	}
	return st, lbl
}

type track17RequestItem struct {
	Number  string `json:"number"`
	Carrier int    `json:"carrier,omitempty"`
}

type track17Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type track17Address struct {
	Country string `json:"country"`
	State   string `json:"state"`
	City    string `json:"city"`
}

type track17AcceptedItem struct {
	Number    string `json:"number"`
	Carrier   int    `json:"carrier"`
	TrackInfo struct {
		LatestStatus struct {
			Status    string `json:"status"`
			SubStatus string `json:"sub_status"`
		} `json:"latest_status"`
		LatestEvent struct {
			TimeISO     string `json:"time_iso"`
			TimeUTC     string `json:"time_utc"`
			Description string `json:"description"`
			Location    string `json:"location"`
		} `json:"latest_event"`
		Tracking struct {
			Providers []struct {
				Provider struct {
					Key  int    `json:"key"`
					Name string `json:"name"`
				} `json:"provider"`
				Events []struct {
					TimeISO     string         `json:"time_iso"`
					TimeUTC     string         `json:"time_utc"`
					Description string         `json:"description"`
					Location    string         `json:"location"`
					Stage       string         `json:"stage"`
					Address     track17Address `json:"address"`
				} `json:"events"`
			} `json:"providers"`
		} `json:"tracking"`
	} `json:"track_info"`
}

type track17Response struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Accepted []track17AcceptedItem `json:"accepted"`
		Rejected []struct {
			Number string       `json:"number"`
			Error  track17Error `json:"error"`
		} `json:"rejected"`
	} `json:"data"`
}
