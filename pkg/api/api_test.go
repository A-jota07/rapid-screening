package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"rapid-screening/pkg/domain"
	"rapid-screening/pkg/geo"
	"rapid-screening/pkg/providers"
	"rapid-screening/pkg/store"
)

// Garante em tempo de compilação que *providers.ProviderChain atende ao contrato de TrackerProvider.
var _ TrackerProvider = (*providers.ProviderChain)(nil)

type mockTracker struct {
	calls   atomic.Int32
	fn      func(ctx context.Context, code string, info domain.CarrierInfo) (*domain.TrackingResult, error)
	healths []domain.ProviderHealth
}

func (m *mockTracker) Track(ctx context.Context, code string, info domain.CarrierInfo) (*domain.TrackingResult, error) {
	m.calls.Add(1)
	if m.fn != nil {
		return m.fn(ctx, code, info)
	}
	return nil, errors.New("not implemented")
}

func (m *mockTracker) Statuses() []domain.ProviderHealth {
	if m.healths != nil {
		return m.healths
	}
	return []domain.ProviderHealth{
		{Name: "free_direct", Enabled: true, Configured: true, Available: true, CreditsRemaining: -1, Mode: "free"},
	}
}

func newTestServer(t *testing.T, tracker TrackerProvider, cacheTTL, cooldown time.Duration, cfg ServerConfig) (*Server, http.Handler) {
	t.Helper()
	st, err := store.NewTrackingStore(filepath.Join(t.TempDir(), "trackings.json"), cacheTTL, cooldown)
	if err != nil {
		t.Fatalf("NewTrackingStore failed: %v", err)
	}
	enricher := geo.NewCatalogEnricher()
	srv := NewServer(st, enricher, tracker, cfg)
	return srv, srv.Handler()
}

func TestHealthAndCarriersEndpointsWithDefaultChain(t *testing.T) {
	chain := providers.NewDefaultChain()
	_, handler := newTestServer(t, chain, 15*time.Minute, 60*time.Second, ServerConfig{
		AllowedOrigins: []string{"http://localhost:5173"},
		RateLimitRPM:   120,
		RateLimitBurst: 20,
	})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// 1. GET /api/v1/health
	resp, err := http.Get(ts.URL + "/api/v1/health")
	if err != nil {
		t.Fatalf("GET /health failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /health status=%d, want 200", resp.StatusCode)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing security header X-Content-Type-Options")
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("expected Cache-Control=no-store on /health, got %q", resp.Header.Get("Cache-Control"))
	}

	var health struct {
		Status    string                  `json:"status"`
		Service   string                  `json:"service"`
		Timestamp time.Time               `json:"timestamp"`
		Cache     domain.CacheStats       `json:"cache"`
		Providers []domain.ProviderHealth `json:"providers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatalf("decode /health failed: %v", err)
	}
	if health.Status != "ok" || health.Service != "rapid-screening-api" {
		t.Fatalf("unexpected health payload: %+v", health)
	}
	if len(health.Providers) == 0 {
		t.Fatal("expected providers list from NewDefaultChain")
	}

	// 2. GET /api/v1/carriers
	respC, err := http.Get(ts.URL + "/api/v1/carriers")
	if err != nil {
		t.Fatalf("GET /carriers failed: %v", err)
	}
	defer respC.Body.Close()
	if respC.StatusCode != http.StatusOK {
		t.Fatalf("GET /carriers status=%d", respC.StatusCode)
	}
	var carriersResp struct {
		Carriers []domain.CarrierInfo `json:"carriers"`
	}
	if err := json.NewDecoder(respC.Body).Decode(&carriersResp); err != nil {
		t.Fatal(err)
	}
	if len(carriersResp.Carriers) < 7 {
		t.Fatalf("expected >=7 supported carriers, got %d", len(carriersResp.Carriers))
	}

	// 3. GET /api/v1/carriers/detect?code=AA123456789BR
	respD, err := http.Get(ts.URL + "/api/v1/carriers/detect?code=aa-123456789-br")
	if err != nil {
		t.Fatalf("GET /carriers/detect failed: %v", err)
	}
	defer respD.Body.Close()
	if respD.StatusCode != http.StatusOK {
		t.Fatalf("GET /carriers/detect status=%d", respD.StatusCode)
	}
	var detectResp struct {
		Code    string             `json:"code"`
		Carrier domain.CarrierInfo `json:"carrier"`
	}
	if err := json.NewDecoder(respD.Body).Decode(&detectResp); err != nil {
		t.Fatal(err)
	}
	if detectResp.Code != "AA123456789BR" || detectResp.Carrier.Code != "correios" {
		t.Fatalf("unexpected detect response: %+v", detectResp)
	}

	// Código ausente -> 400
	respBad, err := http.Get(ts.URL + "/api/v1/carriers/detect")
	if err != nil {
		t.Fatal(err)
	}
	respBad.Body.Close()
	if respBad.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 on missing detect code, got %d", respBad.StatusCode)
	}
}

func TestTrackCacheMissHitAndRefreshCooldown(t *testing.T) {
	now := time.Now().UTC()
	tracker := &mockTracker{
		fn: func(ctx context.Context, code string, info domain.CarrierInfo) (*domain.TrackingResult, error) {
			return &domain.TrackingResult{
				TrackingCode:  code,
				Carrier:       info,
				CurrentStatus: domain.StatusAtCDD,
				ProgressStep:  3,
				ProviderUsed:  "17track",
				Events: []domain.TrackingEvent{
					{
						Timestamp:      now.Add(-30 * time.Minute),
						RawStatus:      "Aguardando retirada",
						StandardStatus: domain.StatusAtCDD,
						Description:    "Objeto aguardando retirada no endereço indicado",
						Location:       "CEE Vila Mariana - São Paulo/SP",
					},
					{
						Timestamp:      now.Add(-24 * time.Hour),
						RawStatus:      "Postado",
						StandardStatus: domain.StatusPosted,
						Description:    "Objeto postado",
						Location:       "Curitiba - PR",
					},
				},
			}, nil
		},
	}

	_, handler := newTestServer(t, tracker, 15*time.Minute, 120*time.Millisecond, ServerConfig{
		RateLimitRPM:   200,
		RateLimitBurst: 50,
	})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// 1. Primeira chamada: Cache MISS -> chama tracker (calls=1), enriquece CDD e RoutePoints.
	resp1, err := http.Get(ts.URL + "/api/v1/track/AA123456789BR")
	if err != nil {
		t.Fatal(err)
	}
	defer resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp1.StatusCode)
	}
	if resp1.Header.Get("X-Cache-Status") != "MISS" {
		t.Errorf("expected X-Cache-Status=MISS, got %q", resp1.Header.Get("X-Cache-Status"))
	}
	if resp1.Header.Get("Cache-Control") != "public, s-maxage=300, stale-while-revalidate=600" {
		t.Errorf("expected Edge Cache-Control header, got %q", resp1.Header.Get("Cache-Control"))
	}

	var res1 domain.TrackingResult
	if err := json.NewDecoder(resp1.Body).Decode(&res1); err != nil {
		t.Fatal(err)
	}
	if res1.Cached || res1.ProviderUsed != "17track" {
		t.Errorf("expected Cached=false and ProviderUsed=17track, got cached=%v provider=%q", res1.Cached, res1.ProviderUsed)
	}
	if res1.CDD == nil || !res1.CDD.AvailableForPickup {
		t.Errorf("expected enriched CDD AvailableForPickup=true, got %+v", res1.CDD)
	}
	if len(res1.RoutePoints) < 2 {
		t.Errorf("expected enriched RoutePoints, got %d", len(res1.RoutePoints))
	}
	if tracker.calls.Load() != 1 {
		t.Fatalf("expected 1 upstream call, got %d", tracker.calls.Load())
	}

	// 2. Segunda chamada: Cache HIT -> não chama tracker (calls=1), retorna Cached=true e ProviderUsed="cache".
	resp2, err := http.Get(ts.URL + "/api/v1/track/aa-123456789-br")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.Header.Get("X-Cache-Status") != "HIT" {
		t.Errorf("expected X-Cache-Status=HIT, got %q", resp2.Header.Get("X-Cache-Status"))
	}
	var res2 domain.TrackingResult
	if err := json.NewDecoder(resp2.Body).Decode(&res2); err != nil {
		t.Fatal(err)
	}
	if !res2.Cached || res2.ProviderUsed != "cache" {
		t.Errorf("expected Cached=true and ProviderUsed=cache, got cached=%v provider=%q", res2.Cached, res2.ProviderUsed)
	}
	if tracker.calls.Load() != 1 {
		t.Fatalf("expected upstream calls to stay 1 on cache hit, got %d", tracker.calls.Load())
	}

	// 3. Refresh dentro do cooldown -> bloqueado pelo cooldown anti-abuso, retorna cache com COOLDOWN-HIT.
	resp3, err := http.Get(ts.URL + "/api/v1/track/AA123456789BR?refresh=true")
	if err != nil {
		t.Fatal(err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK || resp3.Header.Get("X-Cache-Status") != "COOLDOWN-HIT" {
		t.Errorf("expected 200 COOLDOWN-HIT, got status=%d header=%q", resp3.StatusCode, resp3.Header.Get("X-Cache-Status"))
	}
	if resp3.Header.Get("X-Cache-Cooldown-Remaining") == "" {
		t.Error("expected X-Cache-Cooldown-Remaining header")
	}
	var res3 domain.TrackingResult
	if err := json.NewDecoder(resp3.Body).Decode(&res3); err != nil {
		t.Fatal(err)
	}
	if !res3.Cached || res3.ProviderUsed != "cache" {
		t.Errorf("expected cached response on cooldown, got %+v", res3)
	}
	if tracker.calls.Load() != 1 {
		t.Fatalf("cooldown failed to prevent upstream call: calls=%d", tracker.calls.Load())
	}

	// 4. Após expirar cooldown -> ?refresh=true consulta provedor novamente (calls=2).
	time.Sleep(135 * time.Millisecond)
	resp4, err := http.Get(ts.URL + "/api/v1/track/AA123456789BR?refresh=true")
	if err != nil {
		t.Fatal(err)
	}
	defer resp4.Body.Close()
	if resp4.Header.Get("X-Cache-Status") != "MISS" {
		t.Errorf("expected MISS after cooldown expired, got %q", resp4.Header.Get("X-Cache-Status"))
	}
	if tracker.calls.Load() != 2 {
		t.Fatalf("expected 2 upstream calls after cooldown, got %d", tracker.calls.Load())
	}

	// 5. GET /api/v1/trackings/recent deve listar AA123456789BR
	respR, err := http.Get(ts.URL + "/api/v1/trackings/recent")
	if err != nil {
		t.Fatal(err)
	}
	defer respR.Body.Close()
	var recentResp struct {
		Items []domain.RecentTrackingSummary `json:"items"`
	}
	if err := json.NewDecoder(respR.Body).Decode(&recentResp); err != nil {
		t.Fatal(err)
	}
	if len(recentResp.Items) != 1 || recentResp.Items[0].TrackingCode != "AA123456789BR" {
		t.Fatalf("unexpected recent items: %+v", recentResp.Items)
	}
}

func TestWebhooks17TrackAndTrackingMore(t *testing.T) {
	const secret = "super-secret-token"
	tracker := &mockTracker{}
	_, handler := newTestServer(t, tracker, 15*time.Minute, 60*time.Second, ServerConfig{
		WebhookSecret:  secret,
		RateLimitRPM:   200,
		RateLimitBurst: 50,
	})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// 1. Sem secret -> 401 Unauthorized
	reqUnauth, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/webhooks/17track", bytes.NewBufferString(`{"event":"TRACKING_UPDATED","data":{"number":"AA123456789BR"}}`))
	respUnauth, err := http.DefaultClient.Do(reqUnauth)
	if err != nil {
		t.Fatal(err)
	}
	respUnauth.Body.Close()
	if respUnauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without secret, got %d", respUnauth.StatusCode)
	}

	// 2. Payload oficial 17TRACK v2.2 com assinatura SHA256("body/secret") no header "sign"
	payload17 := `{
		"event": "TRACKING_UPDATED",
		"data": {
			"number": "AA987654321BR",
			"carrier": 2151,
			"track_info": {
				"latest_status": {"status": "AvailableForPickup", "sub_status": "AvailableForPickup_Other"},
				"tracking": {
					"providers": [{
						"provider": {"key": 2151, "name": "Correios"},
						"events": [
							{
								"time_iso": "2026-11-18T14:00:00Z",
								"description": "Objeto aguardando retirada no endereço indicado",
								"location": "CDD Cajuru - Curitiba/PR",
								"stage": "AvailableForPickup"
							},
							{
								"time_iso": "2026-11-17T09:00:00Z",
								"description": "Objeto postado",
								"location": "São Paulo - SP",
								"stage": "InfoReceived"
							}
						]
					}]
				}
			}
		}
	}`
	sum := sha256.Sum256([]byte(payload17 + "/" + secret))
	signHex := hex.EncodeToString(sum[:])

	req17, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/webhooks/17track", bytes.NewBufferString(payload17))
	req17.Header.Set("Content-Type", "application/json")
	req17.Header.Set("sign", signHex)

	resp17, err := http.DefaultClient.Do(req17)
	if err != nil {
		t.Fatal(err)
	}
	defer resp17.Body.Close()
	if resp17.StatusCode != http.StatusOK {
		t.Fatalf("17track webhook failed: status=%d", resp17.StatusCode)
	}
	var wh17 struct {
		Received      bool                  `json:"received"`
		TrackingCode  string                `json:"trackingCode"`
		CurrentStatus domain.StandardStatus `json:"currentStatus"`
		EventsCount   int                   `json:"eventsCount"`
	}
	if err := json.NewDecoder(resp17.Body).Decode(&wh17); err != nil {
		t.Fatal(err)
	}
	if !wh17.Received || wh17.TrackingCode != "AA987654321BR" || wh17.CurrentStatus != domain.StatusAtCDD || wh17.EventsCount != 2 {
		t.Fatalf("unexpected 17track webhook response: %+v", wh17)
	}

	// GET /track/{code} após webhook deve retornar do cache já enriquecido sem chamar tracker!
	respGet, err := http.Get(ts.URL + "/api/v1/track/AA987654321BR")
	if err != nil {
		t.Fatal(err)
	}
	defer respGet.Body.Close()
	var tracked domain.TrackingResult
	if err := json.NewDecoder(respGet.Body).Decode(&tracked); err != nil {
		t.Fatal(err)
	}
	if !tracked.Cached || tracked.CurrentStatus != domain.StatusAtCDD || tracked.CDD == nil || !tracked.CDD.AvailableForPickup {
		t.Fatalf("expected cached enriched AT_CDD after webhook, got %+v", tracked)
	}
	if tracker.calls.Load() != 0 {
		t.Fatalf("expected 0 tracker calls when data came via webhook, got %d", tracker.calls.Load())
	}

	// 3. Payload TrackingMore v4 com HMAC-SHA256 e X-Webhook-Secret
	payloadTM := `{
		"code": 200,
		"message": "Success",
		"data": {
			"tracking_number": "LP00123456789012",
			"courier_code": "cainiao",
			"delivery_status": "delivered",
			"destination_info": {
				"trackinfo": [
					{
						"checkpoint_date": "2026-11-18T16:15:00Z",
						"tracking_detail": "Objeto entregue ao destinatário",
						"location": "Salvador - BA",
						"checkpoint_delivery_status": "delivered"
					}
				]
			}
		}
	}`
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payloadTM))
	tmSig := hex.EncodeToString(mac.Sum(nil))

	reqTM, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/webhooks/trackingmore", bytes.NewBufferString(payloadTM))
	reqTM.Header.Set("Content-Type", "application/json")
	reqTM.Header.Set("X-TrackingMore-Signature", tmSig)

	respTM, err := http.DefaultClient.Do(reqTM)
	if err != nil {
		t.Fatal(err)
	}
	defer respTM.Body.Close()
	if respTM.StatusCode != http.StatusOK {
		t.Fatalf("trackingmore webhook failed: status=%d", respTM.StatusCode)
	}
	var whTM struct {
		Received      bool                  `json:"received"`
		TrackingCode  string                `json:"trackingCode"`
		CurrentStatus domain.StandardStatus `json:"currentStatus"`
	}
	if err := json.NewDecoder(respTM.Body).Decode(&whTM); err != nil {
		t.Fatal(err)
	}
	if !whTM.Received || whTM.TrackingCode != "LP00123456789012" || whTM.CurrentStatus != domain.StatusDelivered {
		t.Fatalf("unexpected trackingmore webhook response: %+v", whTM)
	}

	// Também aceita X-Webhook-Secret direto
	reqSimple, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/webhooks/17track", bytes.NewBufferString(`{
		"trackingCode": "LGG84920194BR",
		"events": [{"timestamp":"2026-11-18T17:00:00Z","status":"OutForDelivery","description":"Motorista a caminho","location":"Pinheiros - São Paulo/SP"}]
	}`))
	reqSimple.Header.Set("Content-Type", "application/json")
	reqSimple.Header.Set("X-Webhook-Secret", secret)
	respSimple, err := http.DefaultClient.Do(reqSimple)
	if err != nil {
		t.Fatal(err)
	}
	respSimple.Body.Close()
	if respSimple.StatusCode != http.StatusOK {
		t.Fatalf("simple webhook with X-Webhook-Secret failed: %d", respSimple.StatusCode)
	}
}

func TestCORSAndRateLimiterMiddleware(t *testing.T) {
	_, handler := newTestServer(t, &mockTracker{}, 15*time.Minute, 60*time.Second, ServerConfig{
		AllowedOrigins: []string{"http://localhost:5173"},
		RateLimitRPM:   60,
		RateLimitBurst: 2,
	})

	// Preflight OPTIONS deve responder 204 com headers CORS sem consumir tokens do limiter
	reqOpt := httptest.NewRequest(http.MethodOptions, "/api/v1/health", nil)
	reqOpt.Header.Set("Origin", "http://localhost:5173")
	recOpt := httptest.NewRecorder()
	handler.ServeHTTP(recOpt, reqOpt)
	if recOpt.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for OPTIONS, got %d", recOpt.Code)
	}
	if recOpt.Header().Get("Access-Control-Allow-Origin") != "http://localhost:5173" {
		t.Errorf("missing CORS Allow-Origin header: %q", recOpt.Header().Get("Access-Control-Allow-Origin"))
	}

	// Burst = 2: 1ª e 2ª requisições passam, 3ª leva 429 Too Many Requests
	for i := 1; i <= 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		req.RemoteAddr = "192.0.2.10:12345"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if i <= 2 && rec.Code != http.StatusOK {
			t.Fatalf("request %d expected 200, got %d", i, rec.Code)
		}
		if i == 3 {
			if rec.Code != http.StatusTooManyRequests {
				t.Fatalf("request 3 expected 429, got %d", rec.Code)
			}
			if rec.Header().Get("Retry-After") == "" {
				t.Error("expected Retry-After header on 429")
			}
		}
	}
}

func TestTrackStaleFallbackOnProviderError(t *testing.T) {
	failMode := false
	tracker := &mockTracker{
		fn: func(ctx context.Context, code string, info domain.CarrierInfo) (*domain.TrackingResult, error) {
			if failMode {
				return nil, errors.New("upstream unavailable")
			}
			return &domain.TrackingResult{
				TrackingCode:  code,
				Carrier:       info,
				CurrentStatus: domain.StatusInTransit,
				ProviderUsed:  "17track",
				Events: []domain.TrackingEvent{
					{Timestamp: time.Now().UTC(), StandardStatus: domain.StatusInTransit, Description: "Em trânsito", Location: "Campinas - SP"},
				},
			}, nil
		},
	}

	// TTL 30ms, cooldown 0 -> expira rápido para testar fallback stale quando provedor falha.
	_, handler := newTestServer(t, tracker, 30*time.Millisecond, 0, ServerConfig{
		RateLimitRPM:   200,
		RateLimitBurst: 50,
	})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// Popula cache
	r1, err := http.Get(ts.URL + "/api/v1/track/BR2412345678901")
	if err != nil {
		t.Fatal(err)
	}
	r1.Body.Close()
	if r1.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", r1.StatusCode)
	}

	time.Sleep(50 * time.Millisecond)
	failMode = true

	// Cache expirou, provedor falha -> retorna stale fallback!
	r2, err := http.Get(ts.URL + "/api/v1/track/BR2412345678901")
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Body.Close()
	if r2.StatusCode != http.StatusOK || r2.Header.Get("X-Cache-Status") != "STALE-FALLBACK" {
		t.Fatalf("expected 200 STALE-FALLBACK, got status=%d header=%q", r2.StatusCode, r2.Header.Get("X-Cache-Status"))
	}

	// Código inexistente com provedor falhando -> 502 Bad Gateway
	r3, err := http.Get(ts.URL + "/api/v1/track/NOPE99999999")
	if err != nil {
		t.Fatal(err)
	}
	r3.Body.Close()
	if r3.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502 when no stale cache exists, got %d", r3.StatusCode)
	}
}
