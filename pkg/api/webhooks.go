package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"rapid-screening/pkg/carrier"
	"rapid-screening/pkg/domain"
)

const (
	maxWebhookBodyBytes = 2 << 20 // 2 MiB
	maxWebhookEvents    = 100
)

// Estruturas flexíveis para suportar 17TRACK v2.2 real e payloads simplificados de teste.
type track17WebhookPayload struct {
	Event        string          `json:"event"`
	TrackingCode string          `json:"trackingCode"`
	Number       string          `json:"number"`
	CarrierID    int             `json:"carrier"`
	Events       []track17Event  `json:"events"`
	Data         json.RawMessage `json:"data"`
}

type track17Data struct {
	Number         string            `json:"number"`
	TrackingNumber string            `json:"tracking_number"`
	TrackingCode   string            `json:"trackingCode"`
	Carrier        int               `json:"carrier"`
	CarrierCode    string            `json:"carrierCode"`
	Events         []track17Event    `json:"events"`
	TrackInfo      *track17TrackInfo `json:"track_info"`
}

type track17TrackInfo struct {
	LatestStatus *struct {
		Status    string `json:"status"`
		SubStatus string `json:"sub_status"`
	} `json:"latest_status"`
	LatestEvent *track17Event `json:"latest_event"`
	Tracking    *struct {
		Providers []struct {
			Provider struct {
				Key  int    `json:"key"`
				Name string `json:"name"`
			} `json:"provider"`
			Events []track17Event `json:"events"`
		} `json:"providers"`
	} `json:"tracking"`
}

type track17Event struct {
	TimeISO     string `json:"time_iso"`
	TimeUTC     string `json:"time_utc"`
	Time        string `json:"time"`
	Timestamp   string `json:"timestamp"`
	Date        string `json:"date"`
	Description string `json:"description"`
	Location    string `json:"location"`
	Stage       string `json:"stage"`
	Status      string `json:"status"`
	SubStatus   string `json:"sub_status"`
	RawStatus   string `json:"rawStatus"`
}

// Estruturas flexíveis para suportar TrackingMore v4 real e payloads simplificados de teste.
type trackingMoreWebhookPayload struct {
	Code         int                 `json:"code"`
	Message      string              `json:"message"`
	TrackingCode string              `json:"trackingCode"`
	Number       string              `json:"tracking_number"`
	CourierCode  string              `json:"courier_code"`
	Events       []trackingMoreEvent `json:"events"`
	Data         json.RawMessage     `json:"data"`
}

type trackingMoreData struct {
	TrackingNumber string `json:"tracking_number"`
	TrackingCode   string `json:"trackingCode"`
	Number         string `json:"number"`
	CourierCode    string `json:"courier_code"`
	DeliveryStatus string `json:"delivery_status"`
	OriginInfo     *struct {
		Trackinfo []trackingMoreEvent `json:"trackinfo"`
	} `json:"origin_info"`
	DestinationInfo *struct {
		Trackinfo []trackingMoreEvent `json:"trackinfo"`
	} `json:"destination_info"`
	Trackinfo []trackingMoreEvent `json:"trackinfo"`
	Events    []trackingMoreEvent `json:"events"`
}

type trackingMoreEvent struct {
	CheckpointDate           string `json:"checkpoint_date"`
	Date                     string `json:"Date"`
	Time                     string `json:"time"`
	Timestamp                string `json:"timestamp"`
	TrackingDetail           string `json:"tracking_detail"`
	StatusDescription        string `json:"StatusDescription"`
	Description              string `json:"description"`
	Details                  string `json:"Details"`
	Location                 string `json:"location"`
	LocationCap              string `json:"Location"`
	CheckpointDeliveryStatus string `json:"checkpoint_delivery_status"`
	CheckpointDeliverySub    string `json:"checkpoint_delivery_substatus"`
	Substatus                string `json:"substatus"`
	Status                   string `json:"status"`
	RawStatus                string `json:"rawStatus"`
}

func (s *Server) handleWebhook17Track(w http.ResponseWriter, r *http.Request) {
	body, ok := s.readAndVerifyWebhookBody(w, r, "17track")
	if !ok {
		return
	}

	var payload track17WebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":   "invalid_json",
			"message": "Payload JSON inválido no webhook 17TRACK.",
		})
		return
	}

	var data track17Data
	if len(bytes.TrimSpace(payload.Data)) > 0 && string(bytes.TrimSpace(payload.Data)) != "null" {
		trimmed := bytes.TrimSpace(payload.Data)
		if trimmed[0] == '[' {
			var arr []track17Data
			if err := json.Unmarshal(trimmed, &arr); err == nil && len(arr) > 0 {
				data = arr[0]
			}
		} else if err := json.Unmarshal(trimmed, &data); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error":   "invalid_webhook_data",
				"message": "Campo data inválido no webhook 17TRACK.",
			})
			return
		}
	}

	rawCode := firstNonEmpty(data.Number, data.TrackingNumber, data.TrackingCode, payload.Number, payload.TrackingCode)
	code := carrier.SanitizeCode(rawCode)
	if len(code) < 5 || len(code) > 40 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":   "missing_tracking_code",
			"message": "Número de rastreio ausente ou inválido no webhook 17TRACK.",
		})
		return
	}

	carrierID := data.Carrier
	if carrierID == 0 {
		carrierID = payload.CarrierID
	}
	var providerName string
	rawEvents := make([]track17Event, 0)

	if data.TrackInfo != nil && data.TrackInfo.Tracking != nil {
		for _, prov := range data.TrackInfo.Tracking.Providers {
			if carrierID == 0 && prov.Provider.Key != 0 {
				carrierID = prov.Provider.Key
			}
			if providerName == "" && prov.Provider.Name != "" {
				providerName = prov.Provider.Name
			}
			rawEvents = append(rawEvents, prov.Events...)
		}
	}
	if len(rawEvents) == 0 && data.TrackInfo != nil && data.TrackInfo.LatestEvent != nil {
		rawEvents = append(rawEvents, *data.TrackInfo.LatestEvent)
	}
	if len(rawEvents) == 0 && len(data.Events) > 0 {
		rawEvents = append(rawEvents, data.Events...)
	}
	if len(rawEvents) == 0 && len(payload.Events) > 0 {
		rawEvents = append(rawEvents, payload.Events...)
	}
	if len(rawEvents) == 0 && data.TrackInfo != nil && data.TrackInfo.LatestStatus != nil && data.TrackInfo.LatestStatus.Status != "" {
		rawEvents = append(rawEvents, track17Event{
			TimeISO:     time.Now().UTC().Format(time.RFC3339),
			Stage:       data.TrackInfo.LatestStatus.Status,
			SubStatus:   data.TrackInfo.LatestStatus.SubStatus,
			Description: firstNonEmpty(data.TrackInfo.LatestStatus.SubStatus, data.TrackInfo.LatestStatus.Status),
		})
	}

	carrierHint := resolveWebhookCarrier(code, carrierID, data.CarrierCode, providerName)
	if len(rawEvents) > maxWebhookEvents {
		rawEvents = rawEvents[:maxWebhookEvents]
	}
	domainEvents := make([]domain.TrackingEvent, 0, len(rawEvents))
	for _, ev := range rawEvents {
		ts := parseFlexibleTime(ev.TimeISO, ev.TimeUTC, ev.Time, ev.Timestamp, ev.Date)
		rawStatus := firstNonEmpty(ev.RawStatus, ev.SubStatus, ev.Status, ev.Stage, ev.Description)
		stdStatus, stdLabel := normalizeWebhookStatus(rawStatus, ev.Description)
		domainEvents = append(domainEvents, domain.TrackingEvent{
			Timestamp:      ts,
			RawStatus:      rawStatus,
			StandardStatus: stdStatus,
			StandardLabel:  stdLabel,
			Description:    firstNonEmpty(ev.Description, rawStatus),
			Location:       strings.TrimSpace(ev.Location),
		})
	}

	s.finalizeWebhook(w, code, carrierHint, domainEvents)
}

func (s *Server) handleWebhookTrackingMore(w http.ResponseWriter, r *http.Request) {
	body, ok := s.readAndVerifyWebhookBody(w, r, "trackingmore")
	if !ok {
		return
	}

	var payload trackingMoreWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":   "invalid_json",
			"message": "Payload JSON inválido no webhook TrackingMore.",
		})
		return
	}

	var data trackingMoreData
	if len(bytes.TrimSpace(payload.Data)) > 0 && string(bytes.TrimSpace(payload.Data)) != "null" {
		trimmed := bytes.TrimSpace(payload.Data)
		if trimmed[0] == '[' {
			var arr []trackingMoreData
			if err := json.Unmarshal(trimmed, &arr); err == nil && len(arr) > 0 {
				data = arr[0]
			}
		} else if err := json.Unmarshal(trimmed, &data); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error":   "invalid_webhook_data",
				"message": "Campo data inválido no webhook TrackingMore.",
			})
			return
		}
	}

	rawCode := firstNonEmpty(data.TrackingNumber, data.TrackingCode, data.Number, payload.Number, payload.TrackingCode)
	code := carrier.SanitizeCode(rawCode)
	if len(code) < 5 || len(code) > 40 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":   "missing_tracking_code",
			"message": "Número de rastreio ausente ou inválido no webhook TrackingMore.",
		})
		return
	}

	courierCode := firstNonEmpty(data.CourierCode, payload.CourierCode)
	rawEvents := make([]trackingMoreEvent, 0)
	if data.OriginInfo != nil && len(data.OriginInfo.Trackinfo) > 0 {
		rawEvents = append(rawEvents, data.OriginInfo.Trackinfo...)
	}
	if data.DestinationInfo != nil && len(data.DestinationInfo.Trackinfo) > 0 {
		rawEvents = append(rawEvents, data.DestinationInfo.Trackinfo...)
	}
	if len(rawEvents) == 0 && len(data.Trackinfo) > 0 {
		rawEvents = append(rawEvents, data.Trackinfo...)
	}
	if len(rawEvents) == 0 && len(data.Events) > 0 {
		rawEvents = append(rawEvents, data.Events...)
	}
	if len(rawEvents) == 0 && len(payload.Events) > 0 {
		rawEvents = append(rawEvents, payload.Events...)
	}
	if len(rawEvents) == 0 && data.DeliveryStatus != "" {
		rawEvents = append(rawEvents, trackingMoreEvent{
			CheckpointDate:           time.Now().UTC().Format(time.RFC3339),
			CheckpointDeliveryStatus: data.DeliveryStatus,
			TrackingDetail:           data.DeliveryStatus,
		})
	}

	carrierHint := resolveWebhookCarrier(code, 0, courierCode, "")
	if len(rawEvents) > maxWebhookEvents {
		rawEvents = rawEvents[:maxWebhookEvents]
	}
	domainEvents := make([]domain.TrackingEvent, 0, len(rawEvents))
	for _, ev := range rawEvents {
		ts := parseFlexibleTime(ev.CheckpointDate, ev.Date, ev.Time, ev.Timestamp)
		desc := firstNonEmpty(ev.TrackingDetail, ev.StatusDescription, ev.Description, ev.Details)
		loc := firstNonEmpty(ev.Location, ev.LocationCap)
		rawStatus := firstNonEmpty(ev.RawStatus, ev.CheckpointDeliveryStatus, ev.CheckpointDeliverySub, ev.Substatus, ev.Status, desc)
		stdStatus, stdLabel := normalizeWebhookStatus(rawStatus, desc)
		domainEvents = append(domainEvents, domain.TrackingEvent{
			Timestamp:      ts,
			RawStatus:      rawStatus,
			StandardStatus: stdStatus,
			StandardLabel:  stdLabel,
			Description:    firstNonEmpty(desc, rawStatus),
			Location:       strings.TrimSpace(loc),
		})
	}

	s.finalizeWebhook(w, code, carrierHint, domainEvents)
}

func (s *Server) finalizeWebhook(w http.ResponseWriter, code string, carrierHint domain.CarrierInfo, events []domain.TrackingEvent) {
	updated, err := s.store.UpsertFromWebhook(code, carrierHint, events)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error":   "webhook_upsert_failed",
			"message": err.Error(),
		})
		return
	}

	if s.enricher != nil {
		s.enricher.Enrich(updated)
		if err := s.store.Save(updated); err != nil {
			s.logger.Warn("failed_to_save_enriched_webhook", "code", code, "error", err)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"received":           true,
		"trackingCode":       updated.TrackingCode,
		"currentStatus":      updated.CurrentStatus,
		"currentStatusLabel": updated.CurrentStatusLabel,
		"eventsCount":        len(updated.Events),
	})
}

func (s *Server) secretForWebhookProvider(provider string) string {
	switch provider {
	case "17track":
		if s.track17WebhookSecret != "" {
			return s.track17WebhookSecret
		}
	case "trackingmore":
		if s.trackingMoreWebhookSecret != "" {
			return s.trackingMoreWebhookSecret
		}
	}
	return s.webhookSecret
}

func (s *Server) readAndVerifyWebhookBody(w http.ResponseWriter, r *http.Request, provider string) ([]byte, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBodyBytes))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":   "invalid_body",
			"message": "Falha ao ler corpo da requisição webhook.",
		})
		return nil, false
	}

	secret := s.secretForWebhookProvider(provider)
	allowInsecure := !isProductionEnv() && strings.EqualFold(strings.TrimSpace(os.Getenv("ALLOW_INSECURE_WEBHOOKS")), "true")
	if secret == "" && !allowInsecure {
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error":   "webhook_secret_not_configured",
			"message": "Recebimento de webhooks desabilitado sem WEBHOOK_SECRET configurado.",
		})
		return nil, false
	}

	if secret != "" && !verifyWebhookAuth(r, body, secret, provider) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error":   "unauthorized_webhook",
			"message": "Assinatura criptográfica de webhook ausente ou inválida.",
		})
		return nil, false
	}
	return body, true
}

func isProductionEnv() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("VERCEL_ENV")), "production")
}

func verifyWebhookAuth(r *http.Request, body []byte, secret string, provider string) bool {
	if secret == "" {
		return false
	}

	// 1. Header direto X-Webhook-Secret permitido apenas fora de produção (desenvolvimento/testes).
	if !isProductionEnv() {
		if hdr := strings.TrimSpace(r.Header.Get("X-Webhook-Secret")); hdr != "" {
			if subtle.ConstantTimeCompare([]byte(hdr), []byte(secret)) == 1 {
				return true
			}
		}
	}

	// 2. Assinatura 17TRACK v2.2 ("sign": SHA256(body + "/" + secret) em hex) ou HMAC-SHA256.
	if provider == "17track" || provider == "" {
		if sign := strings.TrimSpace(r.Header.Get("sign")); sign != "" {
			sum := sha256.Sum256(append(append([]byte{}, body...), append([]byte("/"), secret...)...))
			expected17 := hex.EncodeToString(sum[:])
			if subtle.ConstantTimeCompare([]byte(strings.ToLower(sign)), []byte(expected17)) == 1 {
				return true
			}
			if verifyHMACSHA256(body, secret, sign) {
				return true
			}
		}
	}

	// 3. Assinatura HMAC-SHA256 (TrackingMore ou integradores padrão).
	if provider == "trackingmore" || provider == "" {
		for _, headerName := range []string{"X-TrackingMore-Signature", "X-Signature", "signature"} {
			if sig := strings.TrimSpace(r.Header.Get(headerName)); sig != "" {
				if verifyHMACSHA256(body, secret, sig) {
					return true
				}
			}
		}
	}

	return false
}

func verifyHMACSHA256(body []byte, secret, providedHex string) bool {
	provided := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(providedHex)), "sha256=")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

func resolveWebhookCarrier(code string, track17ID int, tmCode string, providerName string) domain.CarrierInfo {
	supported := carrier.ListSupportedCarriers()
	for _, c := range supported {
		if track17ID != 0 && c.Track17CarrierID == track17ID {
			c.DetectedBy = "aggregator"
			c.Confidence = 0.99
			return c
		}
		if tmCode != "" && strings.EqualFold(c.TrackingMoreCode, strings.TrimSpace(tmCode)) {
			c.DetectedBy = "aggregator"
			c.Confidence = 0.99
			return c
		}
	}

	detected := carrier.DetectCarrier(code)
	if detected.Code != "" && detected.Code != "auto" {
		return detected
	}

	if tmCode != "" || providerName != "" {
		name := firstNonEmpty(providerName, tmCode, detected.Name)
		cCode := firstNonEmpty(strings.ToLower(strings.ReplaceAll(tmCode, "-", "_")), detected.Code)
		return domain.CarrierInfo{
			Code:             cCode,
			Name:             name,
			Track17CarrierID: track17ID,
			TrackingMoreCode: tmCode,
			DetectedBy:       "aggregator",
			Confidence:       0.90,
		}
	}
	return detected
}

func normalizeWebhookStatus(rawStatus, description string) (domain.StandardStatus, string) {
	status, label, _ := carrier.NormalizeStatus(rawStatus)
	if description != "" {
		descStatus, descLabel, _ := carrier.NormalizeStatus(description)
		// Se o rawStatus caiu no genérico IN_TRANSIT (ou estava vazio), mas a descrição traz estado específico, prioriza a descrição.
		if (rawStatus == "" || status == domain.StatusInTransit) && descStatus != domain.StatusInTransit {
			return descStatus, descLabel
		}
	}
	return status, label
}

func parseFlexibleTime(candidates ...string) time.Time {
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
	}
	for _, raw := range candidates {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		for _, layout := range layouts {
			if t, err := time.Parse(layout, s); err == nil {
				return t.UTC()
			}
		}
	}
	return time.Now().UTC()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
