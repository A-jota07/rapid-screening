package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	carrierpkg "rapid-screening/pkg/carrier"
	"rapid-screening/pkg/domain"
)

const (
	defaultWoncaBaseURL        = "https://api.wonca.com.br/wonca.labs.v1.LabsService/Track"
	defaultSiteRastreioBaseURL = "https://seurastreio.com.br/api/public/rastreio"
	defaultSiteRastreioCredits = 1000
	defaultSiteRastreioTimeout = 10 * time.Second
	defaultSiteRastreioCircuit = 5 * time.Minute
)

// SiteRastreioConfig configura o provedor Site Rastreio / Wonca Labs (cota gratuita mensal de 1.000 req/mês).
type SiteRastreioConfig struct {
	APIKey          string
	MonthlyCredits  int
	BaseURL         string
	HTTPClient      *http.Client
	CircuitDuration time.Duration
}

// SiteRastreioProvider integra com a API REST do Wonca Labs (labs.wonca.com.br / siterastreio.com.br)
// e também suporta o formato Seu Rastreio ("sr_live_...").
type SiteRastreioProvider struct {
	state           *providerState
	apiKey          string
	baseURL         string
	client          *http.Client
	circuitDuration time.Duration
}

// NewSiteRastreioProviderFromEnv instancia o provedor lendo SITERASTREIO_API_KEY (ou SEURASTREIO_API_KEY / WONCA_API_KEY)
// e SITERASTREIO_MONTHLY_CREDITS (default 1000).
func NewSiteRastreioProviderFromEnv() *SiteRastreioProvider {
	apiKey := strings.TrimSpace(os.Getenv("SITERASTREIO_API_KEY"))
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("SEURASTREIO_API_KEY"))
	}
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("WONCA_API_KEY"))
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
		if strings.HasPrefix(apiKey, "sr_live_") {
			baseURL = defaultSiteRastreioBaseURL
		} else {
			baseURL = defaultWoncaBaseURL
		}
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
	isWonca := (strings.Contains(p.baseURL, "wonca.com.br") || !strings.HasPrefix(p.apiKey, "sr_live_")) &&
		!strings.Contains(p.baseURL, "seurastreio.com.br")

	rawBody, statusCode, err := p.doRequestWithRetry(ctx, cleanCode, "", isWonca)
	if err != nil {
		p.state.refundCredit()
		wrapped := fmt.Errorf("%w: siterastreio network error: %v", ErrUnavailable, err)
		p.state.recordFailure(time.Since(start), wrapped, 0)
		return nil, wrapped
	}

	if res, done, err := p.handleHTTPStatus(start, statusCode, rawBody); done {
		return res, err
	}

	rawEvents, estRaw, extractedMsg,explicitFail := parseFlexibleSiteRastreioPayload(rawBody)

	// Se for Wonca Labs e a chamada sem "carrier" retornou vazio, tenta novamente informando o enum "carrier" da transportadora.
	if isWonca && len(rawEvents) == 0 && !explicitFail {
		if woncaCarrier := mapCarrierToWoncaEnum(carrierInfo.Code); woncaCarrier != "" {
			if retryBody, retryStatus, retryErr := p.doRequestWithRetry(ctx, cleanCode, woncaCarrier, true); retryErr == nil && retryStatus == http.StatusOK {
				if evs2, est2, msg2, _ := parseFlexibleSiteRastreioPayload(retryBody); len(evs2) > 0 {
					rawEvents = evs2
					if est2 != "" {
						estRaw = est2
					}
					if msg2 != "" {
						extractedMsg = msg2
					}
					rawBody = retryBody
				}
			}
		}
	}

	if explicitFail || len(rawEvents) == 0 {
		if isQuotaOrRateError(200, extractedMsg) {
			p.state.exhaustCredits()
			quotaErr := fmt.Errorf("%w: siterastreio quota message: %s", ErrNoCredits, extractedMsg)
			p.state.recordFailure(time.Since(start), quotaErr, p.circuitDuration)
			return nil, quotaErr
		}
		p.state.refundCredit()
		diag := summarizeJSONShape(rawBody, extractedMsg)
		notFoundErr := fmt.Errorf("%w: siterastreio no events (%s)", ErrNotFound, diag)
		p.state.recordFailure(time.Since(start), notFoundErr, 0)
		return nil, notFoundErr
	}

	events := make([]domain.TrackingEvent, 0, len(rawEvents))
	for _, ev := range rawEvents {
		events = append(events, mapSiteRastreioEvent(ev))
	}

	result := &domain.TrackingResult{
		EstimatedDelivery: normalizeSiteRastreioEstimatedDate(estRaw),
		Events:            events,
	}

	for _, ev := range rawEvents {
		if ev.DestinationPoint != nil && (ev.DestinationPoint.City != "" || ev.DestinationPoint.State != "" || ev.DestinationPoint.FacilityName != "") {
			cp := *ev.DestinationPoint
			cp.FacilityType = "DESTINATION"
			result.Destination = &cp
			break
		}
	}

	finalizeTrackingResult(result, cleanCode, carrierInfo, p.Name(), p.state.now())
	p.state.recordSuccess(time.Since(start))
	return result, nil
}

func (p *SiteRastreioProvider) doRequestWithRetry(ctx context.Context, cleanCode, woncaCarrier string, isWonca bool) ([]byte, int, error) {
	buildReq := func() (*http.Request, error) {
		if isWonca {
			bodyMap := map[string]string{"code": cleanCode}
			if woncaCarrier != "" {
				bodyMap["carrier"] = woncaCarrier
			}
			payloadBytes, _ := json.Marshal(bodyMap)
			r, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL, bytes.NewReader(payloadBytes))
			if err == nil {
				r.Close = true
				r.Header.Set("Authorization", "Apikey "+p.apiKey)
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Accept", "application/json")
				r.Header.Set("User-Agent", "rapid-screening/1.0")
			}
			return r, err
		}
		endpoint := fmt.Sprintf("%s/%s", p.baseURL, url.PathEscape(cleanCode))
		r, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err == nil {
			r.Close = true
			r.Header.Set("Authorization", "Bearer "+p.apiKey)
			r.Header.Set("Accept", "application/json")
			r.Header.Set("User-Agent", "rapid-screening/1.0")
		}
		return r, err
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		req, reqErr := buildReq()
		if reqErr != nil {
			return nil, 0, reqErr
		}
		resp, err := p.client.Do(req)
		if err != nil {
			lastErr = err
			p.client.CloseIdleConnections()
			if attempt < 2 && ctx.Err() == nil {
				time.Sleep(time.Duration(attempt+1) * 250 * time.Millisecond)
				continue
			}
			return nil, 0, lastErr
		}
		rawBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			p.client.CloseIdleConnections()
			if attempt < 2 && ctx.Err() == nil {
				time.Sleep(time.Duration(attempt+1) * 250 * time.Millisecond)
				continue
			}
			return nil, 0, lastErr
		}
		return rawBody, resp.StatusCode, nil
	}
	return nil, 0, lastErr
}

func (p *SiteRastreioProvider) handleHTTPStatus(start time.Time, statusCode int, rawBody []byte) (*domain.TrackingResult, bool, error) {
	if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
		p.state.refundCredit()
		authErr := fmt.Errorf("%w: siterastreio unauthorized HTTP %d", ErrUnavailable, statusCode)
		p.state.recordFailure(time.Since(start), authErr, p.circuitDuration)
		return nil, true, authErr
	}

	if statusCode == http.StatusPaymentRequired {
		p.state.exhaustCredits()
		quotaErr := fmt.Errorf("%w: siterastreio HTTP 402 subscription/quota required", ErrNoCredits)
		p.state.recordFailure(time.Since(start), quotaErr, p.circuitDuration)
		return nil, true, quotaErr
	}

	if statusCode == http.StatusTooManyRequests {
		lower := strings.ToLower(string(rawBody))
		if strings.Contains(lower, "mensal") || strings.Contains(lower, "monthly") || strings.Contains(lower, "quota") || strings.Contains(lower, "cota") {
			p.state.exhaustCredits()
			quotaErr := fmt.Errorf("%w: siterastreio HTTP 429 monthly quota exhausted", ErrNoCredits)
			p.state.recordFailure(time.Since(start), quotaErr, p.circuitDuration)
			return nil, true, quotaErr
		}
		p.state.refundCredit()
		rateErr := fmt.Errorf("%w: siterastreio HTTP 429 rate limit", ErrUnavailable)
		p.state.recordFailure(time.Since(start), rateErr, p.circuitDuration)
		return nil, true, rateErr
	}

	if statusCode == http.StatusNotFound {
		p.state.refundCredit()
		notFoundErr := fmt.Errorf("%w: siterastreio HTTP 404 tracking not found", ErrNotFound)
		p.state.recordFailure(time.Since(start), notFoundErr, 0)
		return nil, true, notFoundErr
	}

	if statusCode >= 500 {
		p.state.refundCredit()
		srvErr := fmt.Errorf("%w: siterastreio HTTP %d", ErrUnavailable, statusCode)
		p.state.recordFailure(time.Since(start), srvErr, p.circuitDuration)
		return nil, true, srvErr
	}

	if statusCode != http.StatusOK {
		p.state.refundCredit()
		unexpectedErr := fmt.Errorf("%w: siterastreio unexpected HTTP %d", ErrUnavailable, statusCode)
		p.state.recordFailure(time.Since(start), unexpectedErr, 0)
		return nil, true, unexpectedErr
	}

	return nil, false, nil
}

func mapCarrierToWoncaEnum(code string) string {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "correios":
		return "CARRIER_CORREIOS"
	case "cainiao":
		return "CARRIER_CAINIAO"
	case "jt_express":
		return "CARRIER_JT"
	case "total_express":
		return "CARRIER_TOTAL_EXPRESS"
	default:
		return ""
	}
}

// parseFlexibleSiteRastreioPayload decodifica respostas do Wonca Labs e Site Rastreio / Seu Rastreio
// usando map[string]any para tolerar arrays PHP vazios (ex: "endereco": [], "unidadeDestino": []),
// objetos DateTime do PHP (ex: "dtHrCriado": {"date": "..."}, "dtPrevista": {"date": "..."}) e strings JSON aninhadas.
func parseFlexibleSiteRastreioPayload(rawBody []byte) (events []siteRastreioEvent, estimated string, message string, explicitFail bool) {
	var root any
	if err := json.Unmarshal(rawBody, &root); err != nil {
		return nil, "", "invalid JSON: " + err.Error(), true
	}

	rootMap, ok := root.(map[string]any)
	if !ok {
		if arr, isArr := root.([]any); isArr {
			for _, item := range arr {
				if m, ok := item.(map[string]any); ok {
					evs, est, msg := extractFromObjectMap(m)
					if len(evs) > 0 {
						return evs, est, msg, false
					}
				}
			}
		}
		return nil, "", "unexpected root JSON type", false
	}

	if succ, exists := rootMap["success"]; exists {
		if b, isBool := succ.(bool); isBool && !b {
			explicitFail = true
		}
	}

	events, estimated, message = extractFromObjectMap(rootMap)
	return events, estimated, message, explicitFail
}

func extractFromObjectMap(m map[string]any) (events []siteRastreioEvent, estimated string, message string) {
	if m == nil {
		return nil, "", ""
	}

	message = firstNonEmptyStr(
		anyToString(m["message"]),
		anyToString(m["mensagem"]),
		anyToString(m["error"]),
		anyToString(m["msg"]),
	)
	estimated = firstNonEmptyStr(
		extractDateAny(m["previsaoEntrega"]),
		extractDateAny(m["dtPrevista"]),
		extractDateAny(m["estimatedDelivery"]),
	)

	// 1. Campo "json" (Wonca Labs costuma retornar string JSON ou objeto JSON dentro de "json").
	if rawJSON, exists := m["json"]; exists && rawJSON != nil {
		if innerMap := unwrapNestedJSONObject(rawJSON); innerMap != nil {
			evs, est, msg := extractFromObjectMap(innerMap)
			if est != "" && estimated == "" {
				estimated = est
			}
			if msg != "" && message == "" {
				message = msg
			}
			if len(evs) > 0 {
				return evs, estimated, message
			}
		}
	}

	// 2. Containers aninhados comuns ("payload", "data", "result", "objeto").
	for _, containerKey := range []string{"payload", "data", "result", "objeto", "track", "rastreio"} {
		if sub, exists := m[containerKey]; exists && sub != nil {
			if subMap := unwrapNestedJSONObject(sub); subMap != nil {
				evs, est, msg := extractFromObjectMap(subMap)
				if est != "" && estimated == "" {
					estimated = est
				}
				if msg != "" && message == "" {
					message = msg
				}
				if len(evs) > 0 {
					return evs, estimated, message
				}
			}
		}
	}

	// 3. Array "objetos" (padrão Correios SRO v2: {"objetos": [{"codObjeto": "...", "eventos": [...]}]}).
	if objsRaw, exists := m["objetos"]; exists {
		if objsArr, isArr := objsRaw.([]any); isArr {
			for _, elem := range objsArr {
				if objMap := unwrapNestedJSONObject(elem); objMap != nil {
					evs, est, msg := extractFromObjectMap(objMap)
					if est != "" && estimated == "" {
						estimated = est
					}
					if msg != "" && message == "" {
						message = msg
					}
					if len(evs) > 0 {
						return evs, estimated, message
					}
				}
			}
		}
	}

	// 4. Listas de eventos em chaves conhecidas.
	for _, listKey := range []string{"eventos", "events", "historico", "detailList", "trackinfo", "scans", "checkpoints", "movimentacoes"} {
		if listRaw, exists := m[listKey]; exists && listRaw != nil {
			var arr []any
			if a, isArr := listRaw.([]any); isArr {
				arr = a
			} else if s, isStr := listRaw.(string); isStr && strings.HasPrefix(strings.TrimSpace(s), "[") {
				_ = json.Unmarshal([]byte(s), &arr)
			}
			if len(arr) > 0 {
				parsedList := make([]siteRastreioEvent, 0, len(arr))
				for _, item := range arr {
					if evMap := unwrapNestedJSONObject(item); evMap != nil {
						if ev, ok := parseFlexibleEventMap(evMap); ok {
							parsedList = append(parsedList, ev)
						}
					}
				}
				if len(parsedList) > 0 {
					return parsedList, estimated, message
				}
			}
		}
	}

	// 5. Evento único em "eventoMaisRecente" / "latestEvent" / "ultimoEvento".
	for _, singleKey := range []string{"eventoMaisRecente", "latestEvent", "ultimoEvento", "lastEvent"} {
		if singleRaw, exists := m[singleKey]; exists && singleRaw != nil {
			if evMap := unwrapNestedJSONObject(singleRaw); evMap != nil {
				if ev, ok := parseFlexibleEventMap(evMap); ok {
					return []siteRastreioEvent{ev}, estimated, message
				}
			}
		}
	}

	// 6. Fallback para status simples no próprio objeto (ex: payload.status / payload.latestUpdate no Wonca).
	statusStr := firstNonEmptyStr(anyToString(m["status"]), anyToString(m["situacao"]))
	detailStr := firstNonEmptyStr(anyToString(m["statusDetail"]), anyToString(m["latestUpdate"]), anyToString(m["descricao"]))
	if isMeaningfulTopStatus(statusStr, detailStr) {
		dt := firstNonEmptyStr(
			extractDateAny(m["mailingDate"]),
			extractDateAny(m["updatedAt"]),
			extractDateAny(m["data"]),
		)
		desc := statusStr
		if desc == "" || strings.EqualFold(desc, "found") || strings.EqualFold(desc, "ok") {
			desc = detailStr
		}
		return []siteRastreioEvent{{
			Descricao: desc,
			Detalhe:   detailStr,
			Data:      dt,
			Status:    statusStr,
		}}, estimated, message
	}

	return nil, estimated, message
}

func unwrapNestedJSONObject(v any) map[string]any {
	if v == nil {
		return nil
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	if s, ok := v.(string); ok {
		trimmed := strings.TrimSpace(s)
		if strings.HasPrefix(trimmed, "{") {
			var m map[string]any
			if err := json.Unmarshal([]byte(trimmed), &m); err == nil {
				return m
			}
		}
		if strings.HasPrefix(trimmed, "[") {
			var arr []any
			if err := json.Unmarshal([]byte(trimmed), &arr); err == nil && len(arr) > 0 {
				if m, ok := arr[0].(map[string]any); ok {
					return m
				}
			}
		}
	}
	return nil
}

func isMeaningfulTopStatus(status, detail string) bool {
	s := strings.ToLower(strings.TrimSpace(status))
	d := strings.ToLower(strings.TrimSpace(detail))
	switch s {
	case "", "not_found", "notfound", "error", "failed", "false", "0", "404", "invalid", "empty":
		return false
	case "found", "ok", "true", "success":
		return d != ""
	default:
		return true
	}
}

func parseFlexibleEventMap(m map[string]any) (siteRastreioEvent, bool) {
	codigo := firstNonEmptyStr(
		anyToString(m["codigo"]),
		anyToString(m["code"]),
		anyToString(m["nodeCode"]),
	)
	descricao := firstNonEmptyStr(
		anyToString(m["descricao"]),
		anyToString(m["status"]),
		anyToString(m["desc"]),
		anyToString(m["standerdDesc"]),
		anyToString(m["tracking_detail"]),
		anyToString(m["StatusDescription"]),
		anyToString(m["title"]),
	)
	detalhe := firstNonEmptyStr(
		anyToString(m["detalhe"]),
		anyToString(m["statusDetail"]),
		anyToString(m["Details"]),
		anyToString(m["comentario"]),
		anyToString(m["subStatus"]),
	)
	status := firstNonEmptyStr(
		anyToString(m["status"]),
		descricao,
	)

	if descricao == "" && detalhe == "" && status == "" {
		return siteRastreioEvent{}, false
	}

	dateStr := firstNonEmptyStr(
		extractDateAny(m["dtHrCriado"]),
		extractDateAny(m["data"]),
		extractDateAny(m["date"]),
		extractDateAny(m["timestamp"]),
		extractDateAny(m["time"]),
		extractDateAny(m["checkpoint_date"]),
		extractDateAny(m["createdAt"]),
	)
	timeOnly := strings.TrimSpace(anyToString(m["time"]))
	if timeOnly != "" && dateStr != "" && !strings.Contains(dateStr, ":") && len(dateStr) == 10 {
		dateStr = dateStr + "T" + timeOnly
	}
	horaOnly := strings.TrimSpace(anyToString(m["hora"]))
	if horaOnly != "" && dateStr != "" && !strings.Contains(dateStr, ":") {
		dateStr = dateStr + " " + horaOnly
	}

	loc, unitPt := extractUnitFromAny(m["unidade"])
	if loc == "" || loc == "Brasil" {
		directLoc := firstNonEmptyStr(
			anyToString(m["local"]),
			anyToString(m["location"]),
			anyToString(m["Location"]),
		)
		city := firstNonEmptyStr(anyToString(m["cidade"]), anyToString(m["city"]))
		state := firstNonEmptyStr(anyToString(m["uf"]), anyToString(m["state"]))
		zip := firstNonEmptyStr(anyToString(m["cep"]), anyToString(m["zip"]))

		if directLoc != "" {
			loc = directLoc
		} else if city != "" {
			if state != "" {
				loc = city + " - " + state
			} else {
				loc = city
			}
		}
		if unitPt == nil && (city != "" || state != "" || loc != "") {
			unitPt = &domain.GeoPoint{
				City:         city,
				State:        state,
				ZipCode:      zip,
				FacilityType: "HUB",
				FacilityName: loc,
			}
		}
	}

	var dest string
	var destPt *domain.GeoPoint
	if dLoc, dPt := extractUnitFromAny(m["unidadeDestino"]); dLoc != "" && dLoc != "Brasil" {
		dest = dLoc
		destPt = dPt
	} else if dStr := firstNonEmptyStr(anyToString(m["destino"]), anyToString(m["destination"])); dStr != "" {
		dest = dStr
	}

	if (loc == "" || loc == "Brasil") && dest != "" {
		loc = dest
	}

	return siteRastreioEvent{
		Codigo:           codigo,
		Descricao:        descricao,
		Detalhe:          detalhe,
		Data:             dateStr,
		Local:            loc,
		Destino:          dest,
		Status:           status,
		UnitPoint:        unitPt,
		DestinationPoint: destPt,
	}, true
}

func extractUnitFromAny(v any) (string, *domain.GeoPoint) {
	m := unwrapNestedJSONObject(v)
	if m == nil {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s), nil
		}
		return "", nil
	}

	var u woncaCorreiosUnit
	u.Nome = firstNonEmptyStr(anyToString(m["nome"]), anyToString(m["name"]))
	u.Tipo = firstNonEmptyStr(anyToString(m["tipo"]), anyToString(m["type"]), anyToString(m["tipounidade"]))
	u.Cidade = firstNonEmptyStr(anyToString(m["cidade"]), anyToString(m["city"]))
	u.UF = firstNonEmptyStr(anyToString(m["uf"]), anyToString(m["state"]), anyToString(m["codSro"]))

	if endMap := unwrapNestedJSONObject(m["endereco"]); endMap != nil {
		u.Endereco.Cidade = firstNonEmptyStr(anyToString(endMap["cidade"]), anyToString(endMap["city"]))
		u.Endereco.UF = firstNonEmptyStr(anyToString(endMap["uf"]), anyToString(endMap["state"]))
		u.Endereco.Bairro = firstNonEmptyStr(anyToString(endMap["bairro"]), anyToString(endMap["neighborhood"]))
		u.Endereco.Logradouro = firstNonEmptyStr(anyToString(endMap["logradouro"]), anyToString(endMap["address"]))
		u.Endereco.CEP = firstNonEmptyStr(anyToString(endMap["cep"]), anyToString(endMap["zip"]))
	}

	return extractCorreiosUnitLocation(u)
}

func extractDateAny(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		d := strings.TrimSpace(val)
		if idx := strings.IndexByte(d, '.'); idx > 10 && !strings.HasSuffix(d, "Z") {
			d = d[:idx]
		}
		return d
	case map[string]any:
		d := firstNonEmptyStr(anyToString(val["date"]), anyToString(val["data"]), anyToString(val["iso"]))
		if idx := strings.IndexByte(d, '.'); idx > 10 && !strings.HasSuffix(d, "Z") {
			d = d[:idx]
		}
		return d
	default:
		return ""
	}
}

func anyToString(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return strings.TrimSpace(val)
	case float64:
		return fmt.Sprintf("%.0f", val)
	case json.Number:
		return val.String()
	default:
		return ""
	}
}

func firstNonEmptyStr(candidates ...string) string {
	for _, c := range candidates {
		if trimmed := strings.TrimSpace(c); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func summarizeJSONShape(rawBody []byte, extractedMsg string) string {
	var m map[string]any
	if err := json.Unmarshal(rawBody, &m); err != nil {
		return strings.TrimSpace(extractedMsg)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var innerKeys []string
	if rawJSON, ok := m["json"]; ok {
		if inner := unwrapNestedJSONObject(rawJSON); inner != nil {
			for k := range inner {
				innerKeys = append(innerKeys, k)
			}
			sort.Strings(innerKeys)
		}
	} else if payload, ok := m["payload"]; ok {
		if inner := unwrapNestedJSONObject(payload); inner != nil {
			for k := range inner {
				innerKeys = append(innerKeys, k)
			}
			sort.Strings(innerKeys)
		}
	}

	shape := "keys=" + strings.Join(keys, ",")
	if len(innerKeys) > 0 {
		shape += " inner=" + strings.Join(innerKeys, ",")
	}
	if extractedMsg != "" {
		return extractedMsg + " [" + shape + "]"
	}
	return shape
}

func extractCorreiosUnitLocation(u woncaCorreiosUnit) (string, *domain.GeoPoint) {
	city := strings.TrimSpace(u.Endereco.Cidade)
	if city == "" {
		city = strings.TrimSpace(u.Cidade)
	}
	uf := strings.TrimSpace(u.Endereco.UF)
	if uf == "" {
		uf = strings.TrimSpace(u.UF)
	}
	name := strings.TrimSpace(u.Nome)
	unitType := strings.TrimSpace(u.Tipo)
	bairro := strings.TrimSpace(u.Endereco.Bairro)
	logradouro := strings.TrimSpace(u.Endereco.Logradouro)
	cep := strings.TrimSpace(u.Endereco.CEP)

	loc := formatCorreiosLocation(city, uf, name)
	if name != "" && city != "" && !strings.Contains(strings.ToLower(loc), strings.ToLower(name)) {
		loc = name + " - " + loc
	} else if unitType != "" && city != "" && name == "" {
		loc = unitType + " - " + loc
	} else if loc == "Brasil" {
		switch {
		case name != "" && uf != "":
			loc = name + " - " + uf
		case unitType != "" && bairro != "":
			loc = unitType + " - " + bairro
		case unitType != "" && uf != "":
			loc = unitType + " - " + uf
		case unitType != "":
			loc = unitType
		case bairro != "":
			loc = bairro
		}
	}

	var pt *domain.GeoPoint
	facilityLabel := name
	if facilityLabel == "" {
		facilityLabel = unitType
	}
	if city != "" || uf != "" || facilityLabel != "" || logradouro != "" || cep != "" {
		addr := logradouro
		if bairro != "" && addr != "" && !strings.Contains(strings.ToLower(addr), strings.ToLower(bairro)) {
			addr = addr + " - " + bairro
		} else if addr == "" {
			addr = bairro
		}
		pt = &domain.GeoPoint{
			City:         city,
			State:        uf,
			Address:      addr,
			ZipCode:      cep,
			FacilityType: "HUB",
			FacilityName: facilityLabel,
		}
	}
	return loc, pt
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

	var coords *domain.GeoPoint
	if ev.UnitPoint != nil {
		cp := *ev.UnitPoint
		coords = &cp
	}

	return domain.TrackingEvent{
		Timestamp:      parseTimestamp(ev.Data),
		RawStatus:      rawStatus,
		StandardStatus: stdStatus,
		StandardLabel:  stdLabel,
		Description:    strings.Join(descParts, " — "),
		Location:       loc,
		Coordinates:    coords,
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
	Codigo           string           `json:"codigo"`
	Descricao        string           `json:"descricao"`
	Detalhe          string           `json:"detalhe"`
	Data             string           `json:"data"`
	Local            string           `json:"local"`
	Destino          string           `json:"destino"`
	Status           string           `json:"status"`
	UnitPoint        *domain.GeoPoint `json:"-"`
	DestinationPoint *domain.GeoPoint `json:"-"`
}

type woncaCorreiosUnit struct {
	Nome     string `json:"nome"`
	Tipo     string `json:"tipo"`
	Cidade   string `json:"cidade"`
	UF       string `json:"uf"`
	Endereco struct {
		Cidade     string `json:"cidade"`
		UF         string `json:"uf"`
		Bairro     string `json:"bairro"`
		Logradouro string `json:"logradouro"`
		CEP        string `json:"cep"`
	} `json:"endereco"`
}
