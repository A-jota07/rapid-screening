package store

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"rapid-screening/pkg/domain"
)

func TestStoreSaveGetAndStats(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "trackings.json")
	st, err := NewTrackingStore(filePath, 15*time.Minute, 60*time.Second)
	if err != nil {
		t.Fatalf("NewTrackingStore failed: %v", err)
	}

	// Cache miss inicial
	if got, ok := st.Get("BR-123456789-BR", false); ok || got != nil {
		t.Fatalf("expected cache miss, got %+v", got)
	}

	res := &domain.TrackingResult{
		TrackingCode:  "BR-123456789-BR",
		Carrier:       domain.CarrierInfo{Code: "correios", Name: "Correios", Confidence: 0.99},
		CurrentStatus: domain.StatusInTransit,
		ProviderUsed:  "free_direct",
		CDD:           &domain.CDDAllocation{Name: "CDD Vila Mariana"},
		Events: []domain.TrackingEvent{
			{
				Timestamp:      time.Now().UTC().Add(-1 * time.Hour),
				RawStatus:      "Em trânsito",
				StandardStatus: domain.StatusInTransit,
				Description:    "Objeto encaminhado",
				Location:       "São Paulo - SP",
			},
		},
	}

	if err := st.Save(res); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Cache hit
	got, ok := st.Get("br 123456789 br", false)
	if !ok || got == nil {
		t.Fatal("expected cache hit")
	}
	if !got.Cached {
		t.Error("expected Cached=true from Get")
	}
	if got.TrackingCode != "BR123456789BR" {
		t.Errorf("expected sanitized trackingCode BR123456789BR, got %q", got.TrackingCode)
	}

	// Mutação na cópia retornada não deve corromper L1 cache.
	got.CurrentStatus = domain.StatusDelivered
	got2, ok := st.Get("BR123456789BR", false)
	if !ok || got2.CurrentStatus != domain.StatusInTransit {
		t.Fatalf("cache was mutated externally: %+v", got2)
	}

	stats := st.Stats()
	if stats.Hits != 2 || stats.Misses != 1 || stats.CreditsSaved != 2 || stats.StoredCount != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if stats.CooldownSeconds != 60 {
		t.Errorf("expected CooldownSeconds=60, got %d", stats.CooldownSeconds)
	}
}

func TestDynamicTTLsAndExpiration(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "trackings.json")
	st, err := NewTrackingStore(filePath, 15*time.Minute, 60*time.Second)
	if err != nil {
		t.Fatalf("NewTrackingStore failed: %v", err)
	}

	now := time.Now().UTC()

	cases := []struct {
		code    string
		status  domain.StandardStatus
		wantTTL time.Duration
	}{
		{"TRKPOSTED1", domain.StatusPosted, 15 * time.Minute},
		{"TRKTRANSIT1", domain.StatusInTransit, 15 * time.Minute},
		{"TRKCDD1", domain.StatusAtCDD, 10 * time.Minute},
		{"TRKOUT1", domain.StatusOutForDelivery, 10 * time.Minute},
		{"TRKDELIV1", domain.StatusDelivered, 24 * time.Hour},
	}

	for _, tc := range cases {
		res := &domain.TrackingResult{
			TrackingCode:  tc.code,
			CurrentStatus: tc.status,
			LastUpdatedAt: now,
		}
		if err := st.Save(res); err != nil {
			t.Fatalf("Save %s failed: %v", tc.code, err)
		}
		gotTTL := res.CacheExpiresAt.Sub(res.LastUpdatedAt)
		if gotTTL != tc.wantTTL {
			t.Errorf("status %s: got TTL %v, want %v", tc.status, gotTTL, tc.wantTTL)
		}
	}

	// Testa expiração curta com allowStale=false vs allowStale=true
	shortPath := filepath.Join(t.TempDir(), "short.json")
	shortStore, err := NewTrackingStore(shortPath, 40*time.Millisecond, 0)
	if err != nil {
		t.Fatalf("shortStore failed: %v", err)
	}
	if err := shortStore.Save(&domain.TrackingResult{
		TrackingCode:  "SHORT123",
		CurrentStatus: domain.StatusInTransit,
	}); err != nil {
		t.Fatalf("Save short failed: %v", err)
	}

	time.Sleep(60 * time.Millisecond)

	if _, ok := shortStore.Get("SHORT123", false); ok {
		t.Fatal("expected cache miss after TTL expiration when allowStale=false")
	}
	if got, ok := shortStore.Get("SHORT123", true); !ok || got == nil {
		t.Fatal("expected stale result when allowStale=true")
	}
}

func TestRefreshCooldown(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "trackings.json")
	st, err := NewTrackingStore(filePath, 15*time.Minute, 80*time.Millisecond)
	if err != nil {
		t.Fatalf("NewTrackingStore failed: %v", err)
	}

	can, rem := st.CanRefresh("COOL123")
	if !can || rem != 0 {
		t.Fatalf("expected CanRefresh=true before first save, got can=%v rem=%v", can, rem)
	}

	if err := st.Save(&domain.TrackingResult{
		TrackingCode:  "COOL123",
		CurrentStatus: domain.StatusInTransit,
	}); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	can, rem = st.CanRefresh("COOL123")
	if can || rem <= 0 {
		t.Fatalf("expected CanRefresh=false right after save, got can=%v rem=%v", can, rem)
	}

	time.Sleep(95 * time.Millisecond)

	can, rem = st.CanRefresh("COOL123")
	if !can || rem != 0 {
		t.Fatalf("expected CanRefresh=true after cooldown elapsed, got can=%v rem=%v", can, rem)
	}
}

func TestAtomicDiskPersistenceAndReload(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "nested", "trackings.json")

	st1, err := NewTrackingStore(filePath, 15*time.Minute, 60*time.Second)
	if err != nil {
		t.Fatalf("st1 failed: %v", err)
	}

	_ = st1.Save(&domain.TrackingResult{
		TrackingCode:  "PERSIST1",
		Carrier:       domain.CarrierInfo{Code: "jadlog", Name: "Jadlog"},
		CurrentStatus: domain.StatusAtCDD,
		ProviderUsed:  "17track",
		CDD:           &domain.CDDAllocation{Name: "Unidade Jadlog Mooca"},
	})
	_, _ = st1.Get("PERSIST1", false) // +1 hit / +1 credit saved
	// Persistir stats atualizados no próximo save
	_ = st1.Save(&domain.TrackingResult{
		TrackingCode:  "PERSIST2",
		Carrier:       domain.CarrierInfo{Code: "loggi", Name: "Loggi"},
		CurrentStatus: domain.StatusDelivered,
		ProviderUsed:  "trackingmore",
	})

	// Recarregar em nova instância
	st2, err := NewTrackingStore(filePath, 15*time.Minute, 60*time.Second)
	if err != nil {
		t.Fatalf("reload failed: %v", err)
	}

	got, ok := st2.Get("PERSIST1", false)
	if !ok || got.CDD == nil || got.CDD.Name != "Unidade Jadlog Mooca" {
		t.Fatalf("reloaded item mismatch: ok=%v got=%+v", ok, got)
	}

	can, _ := st2.CanRefresh("PERSIST2")
	if can {
		t.Error("expected cooldown state to survive reload")
	}

	recent := st2.Recent(10)
	if len(recent) != 2 {
		t.Fatalf("expected 2 recent items, got %d", len(recent))
	}
	if recent[0].TrackingCode != "PERSIST2" {
		t.Errorf("expected most recent item PERSIST2 first, got %q", recent[0].TrackingCode)
	}

	// Arquivo JSON inválido deve retornar erro claro
	badFile := filepath.Join(dir, "corrupt.json")
	if err := os.WriteFile(badFile, []byte("{not-json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewTrackingStore(badFile, 15*time.Minute, 60*time.Second); err == nil {
		t.Fatal("expected error loading corrupt json file")
	}
}

func TestUpsertFromWebhookMergesAndRecalculates(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "trackings.json")
	st, err := NewTrackingStore(filePath, 15*time.Minute, 60*time.Second)
	if err != nil {
		t.Fatalf("NewTrackingStore failed: %v", err)
	}

	t1 := time.Now().UTC().Add(-24 * time.Hour)
	t2 := time.Now().UTC().Add(-2 * time.Hour)
	t3 := time.Now().UTC().Add(-10 * time.Minute)

	// 1. Primeiro evento (POSTED)
	res1, err := st.UpsertFromWebhook(
		"WH-12345",
		domain.CarrierInfo{Code: "correios", Name: "Correios", Confidence: 0.9},
		[]domain.TrackingEvent{
			{
				Timestamp:      t1,
				RawStatus:      "Postado",
				StandardStatus: domain.StatusPosted,
				StandardLabel:  domain.StatusLabel(domain.StatusPosted),
				Description:    "Objeto postado",
				Location:       "Rio de Janeiro - RJ",
			},
		},
	)
	if err != nil {
		t.Fatalf("UpsertFromWebhook 1 failed: %v", err)
	}
	if res1.CurrentStatus != domain.StatusPosted || res1.ProgressStep != 1 || res1.ProviderUsed != "webhook" {
		t.Fatalf("unexpected initial webhook state: %+v", res1)
	}

	// 2. Evento DELIVERED chega
	res2, err := st.UpsertFromWebhook(
		"WH-12345",
		domain.CarrierInfo{Code: "correios", Name: "Correios", Confidence: 1.0},
		[]domain.TrackingEvent{
			{
				Timestamp:      t3,
				RawStatus:      "Delivered",
				StandardStatus: domain.StatusDelivered,
				StandardLabel:  domain.StatusLabel(domain.StatusDelivered),
				Description:    "Objeto entregue ao destinatário",
				Location:       "São Paulo - SP",
			},
		},
	)
	if err != nil {
		t.Fatalf("UpsertFromWebhook 2 failed: %v", err)
	}
	if res2.CurrentStatus != domain.StatusDelivered || res2.ProgressStep != 4 || len(res2.Events) != 2 {
		t.Fatalf("unexpected delivered webhook state: %+v", res2)
	}

	// 3. Evento intermediário atrasado (t2) + duplicado (t3) chegam fora de ordem:
	// não deve regredir status DELIVERED nem duplicar t3.
	res3, err := st.UpsertFromWebhook(
		"WH12345",
		domain.CarrierInfo{},
		[]domain.TrackingEvent{
			{
				Timestamp:      t2,
				RawStatus:      "InTransit",
				StandardStatus: domain.StatusInTransit,
				StandardLabel:  domain.StatusLabel(domain.StatusInTransit),
				Description:    "Em trânsito",
				Location:       "Campinas - SP",
			},
			{
				Timestamp:      t3,
				RawStatus:      "Delivered",
				StandardStatus: domain.StatusDelivered,
				StandardLabel:  domain.StatusLabel(domain.StatusDelivered),
				Description:    "Objeto entregue ao destinatário",
				Location:       "São Paulo - SP",
			},
		},
	)
	if err != nil {
		t.Fatalf("UpsertFromWebhook 3 failed: %v", err)
	}
	if len(res3.Events) != 3 {
		t.Fatalf("expected 3 deduplicated events, got %d", len(res3.Events))
	}
	if res3.CurrentStatus != domain.StatusDelivered || res3.ProgressStep != 4 {
		t.Fatalf("expected DELIVERED status preserved, got %s (step %d)", res3.CurrentStatus, res3.ProgressStep)
	}
	if !res3.Events[0].Timestamp.Equal(t3) || !res3.Events[1].Timestamp.Equal(t2) || !res3.Events[2].Timestamp.Equal(t1) {
		t.Errorf("events not sorted newest-first: %+v", res3.Events)
	}
}

func TestBackendModeDetection(t *testing.T) {
	t.Setenv("VERCEL", "")
	t.Setenv("VERCEL_ENV", "")
	t.Setenv("UPSTASH_REDIS_REST_URL", "")
	t.Setenv("UPSTASH_REDIS_REST_TOKEN", "")
	t.Setenv("KV_REST_API_URL", "")
	t.Setenv("KV_REST_API_TOKEN", "")

	filePath := filepath.Join(t.TempDir(), "trackings.json")
	st, err := NewTrackingStore(filePath, 15*time.Minute, 60*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("DATABASE_URL", "")
	t.Setenv("REDIS_URL", "")
	if mode := st.Stats().BackendMode; mode != "memory+json" {
		t.Errorf("got %q, want memory+json", mode)
	}

	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/db")
	if mode := st.Stats().BackendMode; mode != "memory+json (postgres config detected)" {
		t.Errorf("got %q", mode)
	}

	t.Setenv("REDIS_URL", "redis://localhost:6379/0")
	if mode := st.Stats().BackendMode; mode != "memory+json (postgres+redis config detected)" {
		t.Errorf("got %q", mode)
	}

	t.Setenv("DATABASE_URL", "")
	if mode := st.Stats().BackendMode; mode != "memory+json (redis config detected)" {
		t.Errorf("got %q", mode)
	}

	t.Setenv("REDIS_URL", "")
	t.Setenv("VERCEL", "1")
	if mode := st.Stats().BackendMode; mode != "vercel-tmp" {
		t.Errorf("got %q, want vercel-tmp", mode)
	}
}

func TestVercelTempResolutionAndUpstashColdStartHydration(t *testing.T) {
	var mu sync.Mutex
	kv := make(map[string]string)

	upstashMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var cmd []any
		if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil || len(cmd) < 2 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		op, _ := cmd[0].(string)
		key, _ := cmd[1].(string)

		mu.Lock()
		defer mu.Unlock()
		switch op {
		case "SET":
			if len(cmd) >= 3 {
				val, _ := cmd[2].(string)
				kv[key] = val
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": "OK"})
		case "GET":
			val, ok := kv[key]
			if !ok {
				_ = json.NewEncoder(w).Encode(map[string]any{"result": nil})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": val})
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer upstashMock.Close()

	t.Setenv("VERCEL", "1")
	t.Setenv("UPSTASH_REDIS_REST_URL", upstashMock.URL)
	t.Setenv("UPSTASH_REDIS_REST_TOKEN", "test-token")

	// Garante que o caminho padrão "data/trackings.json" é redirecionado para os.TempDir() na Vercel.
	resolved := resolveStoreFilePath(DefaultFilePath)
	if resolved != defaultVercelTempPath() {
		t.Fatalf("expected Vercel temp path %q, got %q", defaultVercelTempPath(), resolved)
	}

	// Instância 1 salva rastreio (grava no Upstash REST).
	path1 := filepath.Join(t.TempDir(), "inst1.json")
	st1, err := NewTrackingStore(path1, 15*time.Minute, 60*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if mode := st1.Stats().BackendMode; mode != "vercel-tmp+upstash-rest" {
		t.Fatalf("expected vercel-tmp+upstash-rest, got %q", mode)
	}

	err = st1.Save(&domain.TrackingResult{
		TrackingCode:  "VERCEL123BR",
		Carrier:       domain.CarrierInfo{Code: "correios", Name: "Correios"},
		CurrentStatus: domain.StatusAtCDD,
		ProviderUsed:  "17track",
		CDD:           &domain.CDDAllocation{Name: "CDD Vila Mariana"},
	})
	if err != nil {
		t.Fatalf("Save on st1 failed: %v", err)
	}

	// Instância 2 simula cold start em container serverless novo com arquivo local vazio.
	path2 := filepath.Join(t.TempDir(), "inst2-empty.json")
	st2, err := NewTrackingStore(path2, 15*time.Minute, 60*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	got, ok := st2.Get("VERCEL123BR", false)
	if !ok || got == nil || got.CDD == nil || got.CDD.Name != "CDD Vila Mariana" {
		t.Fatalf("expected cold-start hydration from Upstash REST, got ok=%v res=%+v", ok, got)
	}

	recent := st2.Recent(5)
	if len(recent) != 1 || recent[0].TrackingCode != "VERCEL123BR" {
		t.Fatalf("expected cold-start recent list from Upstash REST, got %+v", recent)
	}
}

func TestConcurrentStoreAccess(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "concurrent.json")
	st, err := NewTrackingStore(filePath, 15*time.Minute, 60*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			code := "CONC12345"
			if idx%2 == 0 {
				_ = st.Save(&domain.TrackingResult{
					TrackingCode:  code,
					CurrentStatus: domain.StatusInTransit,
				})
			} else {
				_, _ = st.Get(code, false)
			}
			_ = st.Recent(5)
			_ = st.Stats()
			_, _ = st.CanRefresh(code)
		}(i)
	}
	wg.Wait()
}
