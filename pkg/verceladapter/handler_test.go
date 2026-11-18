package verceladapter

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rapid-screening/pkg/domain"
)

func TestVercelServerlessHandler_RewritesCacheAndWebhooks(t *testing.T) {
	// Simula ambiente serverless da Vercel antes do sync.Once inicializar.
	t.Setenv("VERCEL", "1")
	t.Setenv("FREE_DIRECT_ENABLED", "false")
	t.Setenv("DEMO_FALLBACK_ENABLED", "true")
	t.Setenv("TRACK17_API_KEY", "")
	t.Setenv("TRACKINGMORE_API_KEY", "")
	t.Setenv("DATA_FILE", filepath.Join(t.TempDir(), "vercel-trackings.json"))

	// 1. Chamada direta a /api/index.go deve responder health check com BackendMode "vercel-tmp".
	reqHealth := httptest.NewRequest(http.MethodGet, "/api/index.go", nil)
	recHealth := httptest.NewRecorder()
	Handler(recHealth, reqHealth)

	if recHealth.Code != http.StatusOK {
		t.Fatalf("GET /api/index.go status=%d body=%s", recHealth.Code, recHealth.Body.String())
	}

	var healthPayload struct {
		Status string            `json:"status"`
		Cache  domain.CacheStats `json:"cache"`
	}
	if err := json.Unmarshal(recHealth.Body.Bytes(), &healthPayload); err != nil {
		t.Fatalf("decode health failed: %v", err)
	}
	if healthPayload.Status != "ok" {
		t.Fatalf("unexpected health status: %+v", healthPayload)
	}
	if !strings.HasPrefix(healthPayload.Cache.BackendMode, "vercel-tmp") {
		t.Fatalf("expected vercel-tmp backendMode, got %q", healthPayload.Cache.BackendMode)
	}

	// 2. Rewrite da Vercel com ?__path=/api/v1/track/AA123456789BR (Cache MISS + Edge Cache-Control).
	reqTrack1 := httptest.NewRequest(http.MethodGet, "/api/index.go?__path=/api/v1/track/AA123456789BR", nil)
	recTrack1 := httptest.NewRecorder()
	Handler(recTrack1, reqTrack1)

	if recTrack1.Code != http.StatusOK {
		t.Fatalf("track 1 failed: status=%d body=%s", recTrack1.Code, recTrack1.Body.String())
	}
	if cc := recTrack1.Header().Get("Cache-Control"); !strings.Contains(cc, "s-maxage=300") {
		t.Errorf("expected Edge Cache-Control with s-maxage=300, got %q", cc)
	}

	var tr1 domain.TrackingResult
	if err := json.Unmarshal(recTrack1.Body.Bytes(), &tr1); err != nil {
		t.Fatal(err)
	}
	if tr1.TrackingCode != "AA123456789BR" || tr1.Cached {
		t.Fatalf("expected uncached AA123456789BR, got %+v", tr1)
	}

	// 3. Segunda chamada dentro da instância quente -> Cache HIT (0 créditos).
	reqTrack2 := httptest.NewRequest(http.MethodGet, "/api/v1/track/AA123456789BR", nil)
	recTrack2 := httptest.NewRecorder()
	Handler(recTrack2, reqTrack2)

	if recTrack2.Code != http.StatusOK || recTrack2.Header().Get("X-Cache-Status") != "HIT" {
		t.Fatalf("expected HIT on 2nd call, got code=%d cache=%q", recTrack2.Code, recTrack2.Header().Get("X-Cache-Status"))
	}

	// 4. Refresh explícito dentro do cooldown deve retornar Cache-Control: no-store + COOLDOWN-HIT.
	reqRefresh := httptest.NewRequest(http.MethodGet, "/api/index.go?__path=/api/v1/track/AA123456789BR&refresh=true", nil)
	recRefresh := httptest.NewRecorder()
	Handler(recRefresh, reqRefresh)

	if recRefresh.Code != http.StatusOK {
		t.Fatalf("refresh call failed: %d", recRefresh.Code)
	}
	if cc := recRefresh.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("expected Cache-Control=no-store on refresh, got %q", cc)
	}
	if recRefresh.Header().Get("X-Cache-Status") != "COOLDOWN-HIT" {
		t.Errorf("expected COOLDOWN-HIT, got %q", recRefresh.Header().Get("X-Cache-Status"))
	}

	// 5. Webhook reescrito pela Vercel atualiza o status no cache quente (formato 17TRACK v2.2).
	whBody := []byte(`{
		"event": "TRACKING_UPDATED",
		"data": {
			"number": "AA123456789BR",
			"carrier": 2151,
			"events": [
				{
					"description": "Objeto entregue ao destinatário",
					"location": "São Paulo - SP",
					"stage": "Delivered",
					"status": "Entregue ao destinatário"
				}
			]
		}
	}`)
	reqWH := httptest.NewRequest(http.MethodPost, "/api/index.go?__path=/api/v1/webhooks/17track", bytes.NewReader(whBody))
	reqWH.Header.Set("Content-Type", "application/json")
	recWH := httptest.NewRecorder()
	Handler(recWH, reqWH)

	if recWH.Code != http.StatusOK {
		t.Fatalf("webhook failed: status=%d body=%s", recWH.Code, recWH.Body.String())
	}

	reqVerify := httptest.NewRequest(http.MethodGet, "/api/v1/track/AA123456789BR", nil)
	recVerify := httptest.NewRecorder()
	Handler(recVerify, reqVerify)

	var trUpdated domain.TrackingResult
	if err := json.Unmarshal(recVerify.Body.Bytes(), &trUpdated); err != nil {
		t.Fatal(err)
	}
	if trUpdated.CurrentStatus != domain.StatusDelivered {
		t.Fatalf("expected status DELIVERED after webhook, got %s", trUpdated.CurrentStatus)
	}

	_ = os.Remove(os.Getenv("DATA_FILE"))
}
