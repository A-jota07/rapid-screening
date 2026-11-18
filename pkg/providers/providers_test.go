package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	carrierpkg "rapid-screening/pkg/carrier"
	"rapid-screening/pkg/domain"
)

func TestFreeDirectProvider_CorreiosSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/AA123456789BR" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"versao": "2.0",
			"objetos": [{
				"codObjeto": "AA123456789BR",
				"dtPrevista": "2026-11-20",
				"eventos": [
					{
						"dtHrCriado": "2026-11-18T11:30:00-03:00",
						"descricao": "Objeto saiu para entrega ao destinatário",
						"unidade": {"nome": "CDD Vila Mariana", "endereco": {"cidade": "São Paulo", "uf": "SP"}}
					},
					{
						"dtHrCriado": "2026-11-16T09:15:00-03:00",
						"descricao": "Objeto postado",
						"unidade": {"nome": "AC Curitiba", "endereco": {"cidade": "Curitiba", "uf": "PR"}}
					}
				]
			}]
		}`))
	}))
	defer srv.Close()

	p := NewFreeDirectProvider(FreeDirectConfig{
		Enabled: true,
		BaseURL: srv.URL,
		Timeout: 3 * time.Second,
	})

	info := carrierpkg.DetectCarrier("AA123456789BR")
	res, err := p.Track(context.Background(), "AA123456789BR", info)
	if err != nil {
		t.Fatalf("Track failed: %v", err)
	}
	if res.ProviderUsed != "free_direct" {
		t.Errorf("ProviderUsed = %q; want free_direct", res.ProviderUsed)
	}
	if res.CurrentStatus != domain.StatusOutForDelivery {
		t.Errorf("CurrentStatus = %q; want %q", res.CurrentStatus, domain.StatusOutForDelivery)
	}
	if res.ProgressStep != 3 {
		t.Errorf("ProgressStep = %d; want 3", res.ProgressStep)
	}
	if len(res.Events) != 2 {
		t.Fatalf("len(Events) = %d; want 2", len(res.Events))
	}
	if st := p.Status(); st.CreditsRemaining != -1 || !st.Available || st.RequestsUsed != 1 {
		t.Errorf("unexpected status: %+v", st)
	}
}

func TestFreeDirectProvider_OnlySupportsCorreiosAndCircuitBreaker(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	p := NewFreeDirectProvider(FreeDirectConfig{
		Enabled:         true,
		BaseURL:         srv.URL,
		CircuitDuration: time.Minute,
	})

	// Não-Correios não deve chamar endpoint nem contar falha.
	loggiInfo := carrierpkg.DetectCarrier("LGG84920194BR")
	_, err := p.Track(context.Background(), "LGG84920194BR", loggiInfo)
	if !errors.Is(err, ErrUnsupportedCarrier) {
		t.Fatalf("expected ErrUnsupportedCarrier, got %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("expected 0 HTTP hits for non-correios, got %d", hits.Load())
	}

	// Erro 503 abre circuit-breaker.
	correiosInfo := carrierpkg.DetectCarrier("AA123456789BR")
	_, err = p.Track(context.Background(), "AA123456789BR", correiosInfo)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
	if !p.Status().CircuitOpen {
		t.Fatal("expected circuit to be open after 503")
	}

	// Próxima chamada falha imediatamente sem bater na rede.
	_, err = p.Track(context.Background(), "AA123456789BR", correiosInfo)
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("expected still 1 HTTP hit due to open circuit, got %d", hits.Load())
	}

	// Desabilitado via config.
	disabled := NewFreeDirectProvider(FreeDirectConfig{Enabled: false})
	_, err = disabled.Track(context.Background(), "AA123456789BR", correiosInfo)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable when disabled, got %v", err)
	}
}

func TestTrack17Provider_RegisterFlowAndCreditControl(t *testing.T) {
	var getCalls atomic.Int32
	var regCalls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("17token") != "secret-17" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/gettrackinfo":
			n := getCalls.Add(1)
			if n == 1 {
				_, _ = w.Write([]byte(`{
					"code": 0,
					"data": {
						"accepted": [],
						"rejected": [{"number": "LGG84920194BR", "error": {"code": -18019902, "message": "not registered"}}]
					}
				}`))
				return
			}
			_, _ = w.Write([]byte(`{
				"code": 0,
				"data": {
					"accepted": [{
						"number": "LGG84920194BR",
						"carrier": 190271,
						"track_info": {
							"latest_status": {"status": "Delivered"},
							"tracking": {
								"providers": [{
									"provider": {"key": 190271, "name": "Loggi"},
									"events": [{
										"time_iso": "2026-11-18T10:00:00Z",
										"description": "Entrega realizada",
										"stage": "Delivered",
										"address": {"city": "São Paulo", "state": "SP", "country": "BR"}
									}]
								}]
							}
						}
					}]
				}
			}`))
		case "/register":
			regCalls.Add(1)
			_, _ = w.Write([]byte(`{"code": 0, "data": {"accepted": [{"number": "LGG84920194BR"}], "rejected": []}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := NewTrack17Provider(Track17Config{
		APIKey:         "secret-17",
		MonthlyCredits: 2,
		BaseURL:        srv.URL,
	})

	info := carrierpkg.DetectCarrier("LGG84920194BR")
	res, err := p.Track(context.Background(), "LGG84920194BR", info)
	if err != nil {
		t.Fatalf("Track 1 failed: %v", err)
	}
	if regCalls.Load() != 1 || getCalls.Load() != 2 {
		t.Fatalf("expected 1 register and 2 gettrackinfo calls, got reg=%d get=%d", regCalls.Load(), getCalls.Load())
	}
	if res.CurrentStatus != domain.StatusDelivered || res.ProviderUsed != "17track" {
		t.Errorf("unexpected result: status=%q provider=%q", res.CurrentStatus, res.ProviderUsed)
	}
	if st := p.Status(); st.CreditsRemaining != 1 {
		t.Errorf("CreditsRemaining = %d; want 1", st.CreditsRemaining)
	}

	// 2ª chamada consome o último crédito.
	_, err = p.Track(context.Background(), "LGG84920194BR", info)
	if err != nil {
		t.Fatalf("Track 2 failed: %v", err)
	}
	if st := p.Status(); st.CreditsRemaining != 0 || st.Available {
		t.Errorf("expected 0 credits and Available=false, got %+v", st)
	}

	// 3ª chamada falha com ErrNoCredits sem acionar HTTP.
	prevGets := getCalls.Load()
	_, err = p.Track(context.Background(), "LGG84920194BR", info)
	if !errors.Is(err, ErrNoCredits) {
		t.Fatalf("expected ErrNoCredits, got %v", err)
	}
	if getCalls.Load() != prevGets {
		t.Fatal("expected no HTTP call when credits are exhausted")
	}
}

func TestTrack17Provider_HTTP429AndQuotaCircuitBreaker(t *testing.T) {
	srv429 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv429.Close()

	p := NewTrack17Provider(Track17Config{
		APIKey:          "k",
		MonthlyCredits:  10,
		BaseURL:         srv429.URL,
		CircuitDuration: time.Minute,
	})
	info := carrierpkg.DetectCarrier("MND994827162BR")
	_, err := p.Track(context.Background(), "MND994827162BR", info)
	if err == nil {
		t.Fatal("expected error on 429")
	}
	st := p.Status()
	if !st.CircuitOpen || st.Available {
		t.Errorf("expected CircuitOpen=true and Available=false on 429, got %+v", st)
	}
	if st.CreditsRemaining != 10 {
		t.Errorf("expected credit refunded on transient 429, got %d", st.CreditsRemaining)
	}

	// Erro de cota em JSON (-18010012).
	srvQuota := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code": -18010012, "message": "insufficient quota"}`))
	}))
	defer srvQuota.Close()

	p2 := NewTrack17Provider(Track17Config{
		APIKey:          "k",
		MonthlyCredits:  10,
		BaseURL:         srvQuota.URL,
		CircuitDuration: time.Minute,
	})
	_, err = p2.Track(context.Background(), "MND994827162BR", info)
	if !errors.Is(err, ErrNoCredits) {
		t.Fatalf("expected ErrNoCredits on quota error, got %v", err)
	}
	if st2 := p2.Status(); !st2.CircuitOpen || st2.CreditsRemaining != 0 {
		t.Errorf("expected CircuitOpen=true and CreditsRemaining=0, got %+v", st2)
	}
}

func TestTrackingMoreProvider_GetCreateAndQuota(t *testing.T) {
	var created atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Tracking-Api-Key") != "tm-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/trackings/get" {
			_, _ = w.Write([]byte(`{"meta":{"code":4031,"message":"not found"},"data":[]}`))
			return
		}
		if r.URL.Path == "/trackings/create" {
			created.Add(1)
			_, _ = w.Write([]byte(`{
				"meta":{"code":200,"message":"Success"},
				"data":{
					"tracking_number":"JAD1234567890",
					"courier_code":"jadlog",
					"delivery_status":"pickup",
					"origin_info":{
						"courier_code":"jadlog",
						"trackinfo":[
							{"checkpoint_date":"2026-11-18T12:00:00Z","checkpoint_delivery_status":"pickup","tracking_detail":"Disponível para retirada no CDD","city":"Curitiba","state":"PR"},
							{"checkpoint_date":"2026-11-16T08:00:00Z","checkpoint_delivery_status":"inforeceived","tracking_detail":"Etiqueta emitida","city":"São Paulo","state":"SP"}
						]
					}
				}
			}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p := NewTrackingMoreProvider(TrackingMoreConfig{
		APIKey:         "tm-key",
		MonthlyCredits: 50,
		BaseURL:        srv.URL,
	})
	info := carrierpkg.DetectCarrier("JAD1234567890")
	res, err := p.Track(context.Background(), "JAD1234567890", info)
	if err != nil {
		t.Fatalf("Track failed: %v", err)
	}
	if created.Load() != 1 {
		t.Errorf("expected 1 create call, got %d", created.Load())
	}
	if res.ProviderUsed != "trackingmore" || res.CurrentStatus != domain.StatusAtCDD {
		t.Errorf("unexpected result: provider=%q status=%q", res.ProviderUsed, res.CurrentStatus)
	}
	if p.Status().CreditsRemaining != 49 {
		t.Errorf("CreditsRemaining = %d; want 49", p.Status().CreditsRemaining)
	}

	// Teste 429 abre circuit breaker.
	srv429 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv429.Close()

	p429 := NewTrackingMoreProvider(TrackingMoreConfig{
		APIKey:          "tm-key",
		MonthlyCredits:  10,
		BaseURL:         srv429.URL,
		CircuitDuration: time.Minute,
	})
	_, err = p429.Track(context.Background(), "JAD1234567890", info)
	if err == nil || !p429.Status().CircuitOpen {
		t.Fatalf("expected error and open circuit on 429, err=%v status=%+v", err, p429.Status())
	}
}

func TestProviderChain_FallbackWhenProvidersFailOrExhaustCredits(t *testing.T) {
	// 17TRACK retorna 429 -> deve acionar TrackingMore imediatamente.
	srv17 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv17.Close()

	srvTM := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"meta": map[string]any{"code": 200, "message": "Success"},
			"data": []map[string]any{{
				"tracking_number": "AA123456789BR",
				"courier_code":    "brazil-correios",
				"origin_info": map[string]any{
					"trackinfo": []map[string]any{{
						"checkpoint_date":   "2026-11-18T10:00:00Z",
						"tracking_detail":   "Objeto entregue ao destinatário",
						"location":          "São Paulo - SP",
					}},
				},
			}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srvTM.Close()

	freeFail := NewFreeDirectProvider(FreeDirectConfig{Enabled: false})
	t17 := NewTrack17Provider(Track17Config{APIKey: "k17", MonthlyCredits: 5, BaseURL: srv17.URL})
	tm := NewTrackingMoreProvider(TrackingMoreConfig{APIKey: "ktm", MonthlyCredits: 1, BaseURL: srvTM.URL})
	fallback := NewSmartFallbackProvider()

	chain := NewChain([]Provider{freeFail, t17, tm, fallback})

	// 1ª chamada: free_direct desabilitado, 17track retorna 429 (abre circuit), trackingmore responde!
	res1, err := chain.Track(context.Background(), "AA123456789BR", carrierpkg.DetectCarrier("AA123456789BR"))
	if err != nil {
		t.Fatalf("chain Track 1 failed: %v", err)
	}
	if res1.ProviderUsed != "trackingmore" {
		t.Errorf("ProviderUsed = %q; want trackingmore", res1.ProviderUsed)
	}
	if res1.CurrentStatus != domain.StatusDelivered {
		t.Errorf("CurrentStatus = %q; want DELIVERED", res1.CurrentStatus)
	}

	// 2ª chamada: 17track com circuit aberto, trackingmore sem créditos (0 restantes) -> cai no smart_demo_fallback!
	res2, err := chain.Track(context.Background(), "AA123456789BR", carrierpkg.DetectCarrier("AA123456789BR"))
	if err != nil {
		t.Fatalf("chain Track 2 failed: %v", err)
	}
	if res2.ProviderUsed != "smart_demo_fallback" {
		t.Errorf("ProviderUsed = %q; want smart_demo_fallback", res2.ProviderUsed)
	}
	if res2.CurrentStatus != domain.StatusAtCDD || res2.CDD == nil || !res2.CDD.AvailableForPickup {
		t.Errorf("expected AT_CDD with AvailableForPickup=true for AA123456789BR fallback, got status=%q cdd=%+v", res2.CurrentStatus, res2.CDD)
	}
}

func TestNewDefaultChain_OfflineWithoutAPIKeys(t *testing.T) {
	t.Setenv("FREE_DIRECT_ENABLED", "false")
	t.Setenv("SITERASTREIO_API_KEY", "")
	t.Setenv("SEURASTREIO_API_KEY", "")
	t.Setenv("TRACK17_API_KEY", "")
	t.Setenv("TRACKINGMORE_API_KEY", "")
	t.Setenv("SITERASTREIO_MONTHLY_CREDITS", "")
	t.Setenv("TRACK17_MONTHLY_CREDITS", "")
	t.Setenv("TRACKINGMORE_MONTHLY_CREDITS", "")
	t.Setenv("DEMO_FALLBACK_ENABLED", "")

	// 1. Modo produção padrão: sem smart_demo_fallback.
	prodChain := NewDefaultChain()
	prodStatuses := prodChain.Statuses()
	if len(prodStatuses) != 4 {
		t.Fatalf("expected 4 real providers in production default chain, got %d", len(prodStatuses))
	}
	wantProd := []string{"free_direct", "siterastreio", "17track", "trackingmore"}
	for i, wantName := range wantProd {
		if prodStatuses[i].Name != wantName {
			t.Errorf("prodStatuses[%d].Name = %q; want %q", i, prodStatuses[i].Name, wantName)
		}
	}
	if _, err := prodChain.Track(context.Background(), "MND994827162BR", domain.CarrierInfo{}); err == nil {
		t.Fatal("expected error in production mode when no real provider is configured")
	}

	// 2. Modo demo explícito (DEMO_FALLBACK_ENABLED=true): inclui smart_demo_fallback.
	t.Setenv("DEMO_FALLBACK_ENABLED", "true")
	chain := NewDefaultChain()
	statuses := chain.Statuses()
	if len(statuses) != 5 {
		t.Fatalf("expected 5 providers when DEMO_FALLBACK_ENABLED=true, got %d", len(statuses))
	}

	wantOrder := []string{"free_direct", "siterastreio", "17track", "trackingmore", "smart_demo_fallback"}
	for i, wantName := range wantOrder {
		if statuses[i].Name != wantName {
			t.Errorf("statuses[%d].Name = %q; want %q", i, statuses[i].Name, wantName)
		}
	}
	if statuses[1].CreditsRemaining != 1000 || statuses[1].Configured {
		t.Errorf("expected siterastreio default 1000 credits & unconfigured, got %+v", statuses[1])
	}
	if statuses[2].CreditsRemaining != 100 || statuses[2].Configured {
		t.Errorf("expected 17track default 100 credits & unconfigured, got %+v", statuses[2])
	}
	if statuses[3].CreditsRemaining != 50 || statuses[3].Configured {
		t.Errorf("expected trackingmore default 50 credits & unconfigured, got %+v", statuses[3])
	}
	if !statuses[4].Available || statuses[4].CreditsRemaining != -1 {
		t.Errorf("expected smart_demo_fallback available & unlimited, got %+v", statuses[4])
	}

	res, err := chain.Track(context.Background(), "MND994827162BR", domain.CarrierInfo{})
	if err != nil {
		t.Fatalf("default chain offline Track failed: %v", err)
	}
	if res.ProviderUsed != "smart_demo_fallback" {
		t.Errorf("ProviderUsed = %q; want smart_demo_fallback", res.ProviderUsed)
	}
	if res.Carrier.Code != "mandae" {
		t.Errorf("Carrier.Code = %q; want mandae", res.Carrier.Code)
	}
	if res.CurrentStatus != domain.StatusAtCDD {
		t.Errorf("CurrentStatus = %q; want AT_CDD", res.CurrentStatus)
	}
}

func TestSmartFallbackProvider_ScenariosDeterminismAndBrazilGeo(t *testing.T) {
	p := NewSmartFallbackProvider()
	fixedNow := time.Date(2026, 11, 18, 15, 0, 0, 0, time.UTC)
	p.nowFn = func() time.Time { return fixedNow }

	cases := []struct {
		code       string
		wantStatus domain.StandardStatus
		wantPickup bool
	}{
		{"AA123456789BR", domain.StatusAtCDD, true},
		{"MND994827162BR", domain.StatusAtCDD, true},
		{"LOG123456CDD", domain.StatusAtCDD, true},
		{"LGG84920194BR", domain.StatusOutForDelivery, false},
		{"TEX123456ROTA", domain.StatusOutForDelivery, false},
		{"MGL339104857BR", domain.StatusDelivered, false},
		{"JAD1234567890ENT", domain.StatusDelivered, false},
		{"AZL577901234BR", domain.StatusInTransit, false},
	}

	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			info := carrierpkg.DetectCarrier(tc.code)
			r1, err := p.Track(context.Background(), tc.code, info)
			if err != nil {
				t.Fatalf("Track failed: %v", err)
			}
			r2, err := p.Track(context.Background(), tc.code, info)
			if err != nil {
				t.Fatalf("Track 2 failed: %v", err)
			}

			if r1.CurrentStatus != tc.wantStatus {
				t.Errorf("CurrentStatus = %q; want %q", r1.CurrentStatus, tc.wantStatus)
			}
			if r1.CDD == nil {
				t.Fatal("expected CDD to be populated")
			}
			if r1.CDD.AvailableForPickup != tc.wantPickup {
				t.Errorf("CDD.AvailableForPickup = %v; want %v", r1.CDD.AvailableForPickup, tc.wantPickup)
			}
			if r1.Origin == nil || r1.Destination == nil || r1.CurrentLocation == nil {
				t.Fatal("Origin, Destination and CurrentLocation must be populated")
			}
			if len(r1.RoutePoints) < 3 {
				t.Fatalf("expected >= 3 RoutePoints, got %d", len(r1.RoutePoints))
			}
			if len(r1.Events) == 0 {
				t.Fatal("expected non-empty Events")
			}

			// Verifica coordenadas brasileiras válidas em todos os pontos.
			assertBrazilPoint := func(label string, pt domain.GeoPoint) {
				t.Helper()
				if pt.Lat < -34.0 || pt.Lat > 6.0 || pt.Lng < -75.0 || pt.Lng > -34.0 {
					t.Errorf("%s out of Brazil bounds: lat=%v lng=%v", label, pt.Lat, pt.Lng)
				}
			}
			assertBrazilPoint("Origin", *r1.Origin)
			assertBrazilPoint("Destination", *r1.Destination)
			assertBrazilPoint("CurrentLocation", *r1.CurrentLocation)
			assertBrazilPoint("CDD", domain.GeoPoint{Lat: r1.CDD.Lat, Lng: r1.CDD.Lng})
			for i, rp := range r1.RoutePoints {
				assertBrazilPoint("RoutePoint", rp)
				if rp.City == "" || rp.State == "" {
					t.Errorf("RoutePoint[%d] missing city/state: %+v", i, rp)
				}
			}

			// Determinismo entre chamadas.
			if r1.Origin.City != r2.Origin.City || r1.Destination.City != r2.Destination.City ||
				len(r1.Events) != len(r2.Events) || r1.Events[0].RawStatus != r2.Events[0].RawStatus ||
				!r1.Events[0].Timestamp.Equal(r2.Events[0].Timestamp) {
				t.Errorf("non-deterministic output between calls for %s", tc.code)
			}
		})
	}
}

func TestProviderChain_ConcurrentAccess(t *testing.T) {
	chain := NewChain([]Provider{
		NewFreeDirectProvider(FreeDirectConfig{Enabled: false}),
		NewSmartFallbackProvider(),
	})

	codes := []string{"AA123456789BR", "MND994827162BR", "LGG84920194BR", "MGL339104857BR", "AZL577901234BR"}
	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			code := codes[idx%len(codes)]
			res, err := chain.Track(context.Background(), code, carrierpkg.DetectCarrier(code))
			if err != nil {
				t.Errorf("concurrent Track(%s) error: %v", code, err)
				return
			}
			if res.TrackingCode != code {
				t.Errorf("got code %q; want %q", res.TrackingCode, code)
			}
			_ = chain.Statuses()
		}(i)
	}
	wg.Wait()
}

func TestSiteRastreioProvider_HistoryAndLatestOnlyFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sr_live_test_123" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/AA123456789BR":
			_, _ = w.Write([]byte(`{
				"codigo": "AA123456789BR",
				"status": "found",
				"success": true,
				"previsaoEntrega": "2026-11-22T18:00:00.000Z",
				"eventoMaisRecente": {
					"codigo": "LDI",
					"descricao": "Objeto aguardando retirada no endereço indicado",
					"detalhe": "CDD Vila Mariana",
					"data": "2026-11-18T10:15:00.000Z",
					"local": "São Paulo/SP"
				},
				"historico": [
					{
						"codigo": "LDI",
						"descricao": "Objeto aguardando retirada no endereço indicado",
						"detalhe": "CDD Vila Mariana",
						"data": "2026-11-18T10:15:00.000Z",
						"local": "São Paulo/SP"
					},
					{
						"codigo": "RO",
						"descricao": "Objeto em trânsito - por favor aguarde",
						"detalhe": "Unidade de Tratamento",
						"data": "2026-11-16T08:00:00.000Z",
						"local": "Curitiba/PR",
						"destino": "São Paulo/SP"
					}
				]
			}`))
		case "/NL987654321BR":
			// Plano gratuito padrão retorna apenas eventoMaisRecente.
			_, _ = w.Write([]byte(`{
				"codigo": "NL987654321BR",
				"status": "found",
				"success": true,
				"eventoMaisRecente": {
					"codigo": "OEC",
					"descricao": "Objeto saiu para entrega ao destinatário",
					"detalhe": "Em rota local",
					"data": "2026-11-18T12:00:00.000Z",
					"local": "Belo Horizonte/MG"
				}
			}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"success":false,"message":"Objeto não encontrado"}`))
		}
	}))
	defer srv.Close()

	p := NewSiteRastreioProvider(SiteRastreioConfig{
		APIKey:         "sr_live_test_123",
		MonthlyCredits: 1000,
		BaseURL:        srv.URL,
	})

	// 1. Histórico completo + previsão de entrega.
	res1, err := p.Track(context.Background(), "AA123456789BR", carrierpkg.DetectCarrier("AA123456789BR"))
	if err != nil {
		t.Fatalf("Track AA123456789BR failed: %v", err)
	}
	if res1.ProviderUsed != "siterastreio" {
		t.Errorf("ProviderUsed = %q; want siterastreio", res1.ProviderUsed)
	}
	if res1.CurrentStatus != domain.StatusAtCDD || res1.ProgressStep != 3 {
		t.Errorf("got status=%q step=%d; want AT_CDD step=3", res1.CurrentStatus, res1.ProgressStep)
	}
	if res1.EstimatedDelivery != "2026-11-22" {
		t.Errorf("EstimatedDelivery = %q; want 2026-11-22", res1.EstimatedDelivery)
	}
	if len(res1.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(res1.Events))
	}

	// 2. Plano gratuito apenas com eventoMaisRecente.
	res2, err := p.Track(context.Background(), "NL987654321BR", carrierpkg.DetectCarrier("NL987654321BR"))
	if err != nil {
		t.Fatalf("Track NL987654321BR failed: %v", err)
	}
	if res2.CurrentStatus != domain.StatusOutForDelivery || len(res2.Events) != 1 {
		t.Fatalf("unexpected free-tier result: status=%q events=%d", res2.CurrentStatus, len(res2.Events))
	}

	if st := p.Status(); st.CreditsRemaining != 998 || !st.Available {
		t.Errorf("expected 998 credits remaining, got %+v", st)
	}

	// 3. HTTP 404 devolve crédito e não abre circuit-breaker.
	_, err = p.Track(context.Background(), "BR999999999XX", domain.CarrierInfo{})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound on 404, got %v", err)
	}
	if st := p.Status(); st.CreditsRemaining != 998 || st.CircuitOpen {
		t.Errorf("expected credit refunded (998) and circuit closed on 404, got %+v", st)
	}
}

func TestSiteRastreioProvider_QuotaExhaustionTriggersChainFallback(t *testing.T) {
	srSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"monthly_quota_exceeded","message":"Cota mensal de 1000 requisições atingida"}`))
	}))
	defer srSrv.Close()

	sr := NewSiteRastreioProvider(SiteRastreioConfig{
		APIKey:          "sr_live_test_123",
		MonthlyCredits:  1000,
		BaseURL:         srSrv.URL,
		CircuitDuration: 5 * time.Minute,
	})

	chain := NewChain([]Provider{
		sr,
		NewSmartFallbackProvider(),
	})

	res, err := chain.Track(context.Background(), "AA123456789BR", carrierpkg.DetectCarrier("AA123456789BR"))
	if err != nil {
		t.Fatalf("chain fallback failed: %v", err)
	}
	if res.ProviderUsed != "smart_demo_fallback" {
		t.Errorf("expected fallback to smart_demo_fallback, got %q", res.ProviderUsed)
	}
	if st := sr.Status(); !st.CircuitOpen || st.CreditsRemaining != 0 || st.Available {
		t.Errorf("expected siterastreio circuit open & 0 credits, got %+v", st)
	}
}
