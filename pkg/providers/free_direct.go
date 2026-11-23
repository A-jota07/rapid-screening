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
	defaultFreeDirectBaseURL = "https://proxyapp.correios.com.br/v1/sro-rastro"
	defaultCainiaoPublicURL  = "https://global.cainiao.com/global/detail.json"
	defaultFreeDirectTimeout = 4 * time.Second
	defaultFreeDirectCircuit = 60 * time.Second
)

// FreeDirectConfig configura o provedor gratuito direto para Correios.
type FreeDirectConfig struct {
	Enabled         bool
	BaseURL         string
	HTTPClient      *http.Client
	Timeout         time.Duration
	CircuitDuration time.Duration
}

// FreeDirectProvider consulta o endpoint público gratuito dos Correios sem gastar créditos.
type FreeDirectProvider struct {
	state           *providerState
	baseURL         string
	client          *http.Client
	timeout         time.Duration
	circuitDuration time.Duration
}

// NewFreeDirectProviderFromEnv cria o provedor lendo FREE_DIRECT_ENABLED (habilitado por padrão).
func NewFreeDirectProviderFromEnv() *FreeDirectProvider {
	enabled := !strings.EqualFold(strings.TrimSpace(os.Getenv("FREE_DIRECT_ENABLED")), "false")
	baseURL := strings.TrimSpace(os.Getenv("FREE_DIRECT_BASE_URL"))
	return NewFreeDirectProvider(FreeDirectConfig{
		Enabled: enabled,
		BaseURL: baseURL,
	})
}

// NewFreeDirectProvider instancia um FreeDirectProvider com configuração explícita.
func NewFreeDirectProvider(cfg FreeDirectConfig) *FreeDirectProvider {
	baseURL := sanitizeProviderBaseURL(cfg.BaseURL, defaultFreeDirectBaseURL)
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultFreeDirectTimeout
	}
	client := cfg.HTTPClient
	if client == nil {
		client = newSafeHTTPClient(timeout)
	}
	circuitDur := cfg.CircuitDuration
	if circuitDur <= 0 {
		circuitDur = defaultFreeDirectCircuit
	}

	return &FreeDirectProvider{
		state:           newProviderState("free_direct", "free", cfg.Enabled, true, -1),
		baseURL:         baseURL,
		client:          client,
		timeout:         timeout,
		circuitDuration: circuitDur,
	}
}

// Name retorna o identificador do provedor.
func (p *FreeDirectProvider) Name() string {
	return "free_direct"
}

// Status retorna a saúde e disponibilidade atual do provedor gratuito.
func (p *FreeDirectProvider) Status() domain.ProviderHealth {
	return p.state.health()
}

// Track consulta gratuitamente o rastreamento de objetos dos Correios.
func (p *FreeDirectProvider) Track(ctx context.Context, code string, carrierInfo domain.CarrierInfo) (*domain.TrackingResult, error) {
	cleanCode := carrierpkg.SanitizeCode(code)
	if cleanCode == "" {
		return nil, fmt.Errorf("%w: empty tracking code", ErrNotFound)
	}
	if carrierInfo.Code != "correios" && carrierInfo.Code != "cainiao" {
		return nil, fmt.Errorf("%w: free_direct supports only correios and cainiao", ErrUnsupportedCarrier)
	}

	if err := p.state.checkReady(); err != nil {
		return nil, err
	}

	start := time.Now()
	reqCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	// Se for Cainiao puro e usando URL padrão, vai direto no endpoint público global da Cainiao.
	if carrierInfo.Code == "cainiao" && p.baseURL == defaultFreeDirectBaseURL {
		if res, cErr := p.trackViaCainiaoPublic(reqCtx, cleanCode, carrierInfo); cErr == nil && res != nil {
			p.state.recordSuccess(time.Since(start))
			return res, nil
		} else {
			p.state.recordFailure(time.Since(start), cErr, 0)
			return nil, cErr
		}
	}

	endpoint := fmt.Sprintf("%s/%s", p.baseURL, url.PathEscape(cleanCode))
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		p.state.recordFailure(time.Since(start), err, 0)
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "rapid-screening/1.0")

	resp, err := p.client.Do(req)
	if err != nil {
		if p.baseURL == defaultFreeDirectBaseURL {
			if res, cErr := p.trackViaCainiaoPublic(reqCtx, cleanCode, carrierInfo); cErr == nil && res != nil {
				p.state.recordSuccess(time.Since(start))
				return res, nil
			}
		}
		p.state.recordFailure(time.Since(start), err, p.circuitDuration)
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		p.state.recordFailure(time.Since(start), err, p.circuitDuration)
		return nil, fmt.Errorf("%w: failed reading response: %v", ErrUnavailable, err)
	}

	if resp.StatusCode != http.StatusOK && p.baseURL == defaultFreeDirectBaseURL {
		if res, cErr := p.trackViaCainiaoPublic(reqCtx, cleanCode, carrierInfo); cErr == nil && res != nil {
			p.state.recordSuccess(time.Since(start))
			return res, nil
		}
	}

	if resp.StatusCode == http.StatusNotFound {
		errNotFound := fmt.Errorf("%w: correios returned 404", ErrNotFound)
		p.state.recordFailure(time.Since(start), errNotFound, 0)
		return nil, errNotFound
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden || resp.StatusCode >= 500 {
		errHTTP := fmt.Errorf("%w: correios HTTP %d", ErrUnavailable, resp.StatusCode)
		p.state.recordFailure(time.Since(start), errHTTP, p.circuitDuration)
		return nil, errHTTP
	}
	if resp.StatusCode != http.StatusOK {
		errHTTP := fmt.Errorf("%w: correios unexpected HTTP %d", ErrUnavailable, resp.StatusCode)
		p.state.recordFailure(time.Since(start), errHTTP, 0)
		return nil, errHTTP
	}

	var payload correiosSroResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		errJSON := fmt.Errorf("%w: invalid correios JSON: %v", ErrUnavailable, err)
		p.state.recordFailure(time.Since(start), errJSON, p.circuitDuration)
		return nil, errJSON
	}

	if len(payload.Objetos) == 0 {
		errEmpty := fmt.Errorf("%w: correios returned no objects", ErrNotFound)
		p.state.recordFailure(time.Since(start), errEmpty, 0)
		return nil, errEmpty
	}

	obj := payload.Objetos[0]
	if len(obj.Eventos) == 0 {
		msg := strings.TrimSpace(obj.Mensagem)
		if msg == "" {
			msg = "correios object has no events"
		}
		errNoEvents := fmt.Errorf("%w: %s", ErrNotFound, msg)
		p.state.recordFailure(time.Since(start), errNoEvents, 0)
		return nil, errNoEvents
	}

	events := make([]domain.TrackingEvent, 0, len(obj.Eventos))
	for _, ev := range obj.Eventos {
		rawStatus := strings.TrimSpace(ev.Descricao)
		if rawStatus == "" {
			rawStatus = strings.TrimSpace(ev.Detalhe)
		}
		stdStatus, stdLabel, _ := carrierpkg.NormalizeStatus(rawStatus)
		loc := formatCorreiosLocation(ev.Unidade.Endereco.Cidade, ev.Unidade.Endereco.UF, ev.Unidade.Nome)
		desc := rawStatus
		if ev.Detalhe != "" && ev.Detalhe != rawStatus {
			desc = rawStatus + " - " + ev.Detalhe
		}

		var point *domain.GeoPoint
		if ev.Unidade.Endereco.Cidade != "" || ev.Unidade.Endereco.UF != "" {
			point = &domain.GeoPoint{
				City:         ev.Unidade.Endereco.Cidade,
				State:        ev.Unidade.Endereco.UF,
				Address:      ev.Unidade.Endereco.Logradouro,
				ZipCode:      ev.Unidade.Endereco.CEP,
				FacilityType: "HUB",
				FacilityName: ev.Unidade.Nome,
			}
		}

		events = append(events, domain.TrackingEvent{
			Timestamp:      parseTimestamp(ev.DtHrCriado),
			RawStatus:      rawStatus,
			StandardStatus: stdStatus,
			StandardLabel:  stdLabel,
			Description:    desc,
			Location:       loc,
			Coordinates:    point,
		})
	}

	result := &domain.TrackingResult{
		EstimatedDelivery: strings.TrimSpace(obj.DtPrevista),
		Events:            events,
	}
	finalizeTrackingResult(result, cleanCode, carrierInfo, p.Name(), time.Now())
	if len(result.Events) > 0 {
		if result.Events[0].Coordinates != nil {
			curr := *result.Events[0].Coordinates
			result.CurrentLocation = &curr
		}
		last := result.Events[len(result.Events)-1]
		if last.Coordinates != nil {
			orig := *last.Coordinates
			orig.FacilityType = "ORIGIN"
			result.Origin = &orig
		}
	}

	p.state.recordSuccess(time.Since(start))
	return result, nil
}

func (p *FreeDirectProvider) trackViaCainiaoPublic(ctx context.Context, cleanCode string, carrierInfo domain.CarrierInfo) (*domain.TrackingResult, error) {
	endpoint := fmt.Sprintf("%s?mailNos=%s&lang=pt-BR", defaultCainiaoPublicURL, url.QueryEscape(cleanCode))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; rapid-screening/1.0)")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: cainiao network error: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: cainiao HTTP %d", ErrUnavailable, resp.StatusCode)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: cainiao read body: %v", ErrUnavailable, err)
	}

	evs, est, msg, fail := parseFlexibleSiteRastreioPayload(raw)
	if fail || len(evs) == 0 {
		var cainiaoResp struct {
			Module []struct {
				DetailList []struct {
					Time         string `json:"time"`
					Desc         string `json:"desc"`
					StanderdDesc string `json:"standerdDesc"`
					ActionCode   string `json:"actionCode"`
				} `json:"detailList"`
			} `json:"module"`
		}
		if err := json.Unmarshal(raw, &cainiaoResp); err == nil && len(cainiaoResp.Module) > 0 && len(cainiaoResp.Module[0].DetailList) > 0 {
			for _, d := range cainiaoResp.Module[0].DetailList {
				desc := strings.TrimSpace(d.Desc)
				if desc == "" {
					desc = strings.TrimSpace(d.StanderdDesc)
				}
				if desc == "" {
					continue
				}
				evs = append(evs, siteRastreioEvent{
					Codigo:    d.ActionCode,
					Descricao: desc,
					Detalhe:   d.StanderdDesc,
					Data:      d.Time,
					Local:     "Brasil",
				})
			}
		}
	}

	if len(evs) == 0 {
		return nil, fmt.Errorf("%w: cainiao no events (%s)", ErrNotFound, msg)
	}

	events := make([]domain.TrackingEvent, 0, len(evs))
	for _, ev := range evs {
		events = append(events, mapSiteRastreioEvent(ev))
	}

	result := &domain.TrackingResult{
		EstimatedDelivery: normalizeSiteRastreioEstimatedDate(est),
		Events:            events,
	}
	finalizeTrackingResult(result, cleanCode, carrierInfo, p.Name(), time.Now())
	return result, nil
}

func formatCorreiosLocation(city, uf, unitName string) string {
	city = strings.TrimSpace(city)
	uf = strings.TrimSpace(uf)
	unitName = strings.TrimSpace(unitName)

	switch {
	case city != "" && uf != "":
		return city + " - " + uf
	case city != "":
		return city
	case unitName != "":
		return unitName
	default:
		return "Brasil"
	}
}

type correiosSroResponse struct {
	Versao  string `json:"versao"`
	Objetos []struct {
		CodObjeto  string `json:"codObjeto"`
		Mensagem   string `json:"mensagem"`
		DtPrevista string `json:"dtPrevista"`
		Eventos    []struct {
			Codigo     string `json:"codigo"`
			Tipo       string `json:"tipo"`
			DtHrCriado string `json:"dtHrCriado"`
			Descricao  string `json:"descricao"`
			Detalhe    string `json:"detalhe"`
			Unidade    struct {
				Nome     string `json:"nome"`
				Tipo     string `json:"tipo"`
				Endereco struct {
					Cidade     string `json:"cidade"`
					UF         string `json:"uf"`
					Bairro     string `json:"bairro"`
					Logradouro string `json:"logradouro"`
					CEP        string `json:"cep"`
				} `json:"endereco"`
			} `json:"unidade"`
		} `json:"eventos"`
	} `json:"objetos"`
}
