package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"rapid-screening/pkg/domain"
)

const (
	// DefaultFilePath é o caminho padrão de persistência L2 em disco.
	DefaultFilePath = "data/trackings.json"
	// DefaultVercelFileName é o arquivo em /tmp usado automaticamente em ambiente serverless (Vercel).
	DefaultVercelFileName = "rapid_screening_trackings.json"
	// DefaultCacheTTL é o TTL base para encomendas em trânsito ou postadas.
	DefaultCacheTTL = 15 * time.Minute
	// DefaultRefreshCooldown é a janela mínima anti-desperdício entre chamadas upstream para o mesmo código.
	DefaultRefreshCooldown = 60 * time.Second
	// maxStoreEntries limita o total de itens mantidos em memória/disco para evitar OOM e esgotamento de /tmp.
	maxStoreEntries = 1000
)

type cacheEntry struct {
	Result      *domain.TrackingResult `json:"result"`
	LastFetchAt time.Time              `json:"lastFetchAt"`
}

type persistedStats struct {
	Hits         int64 `json:"hits"`
	Misses       int64 `json:"misses"`
	CreditsSaved int64 `json:"creditsSaved"`
}

type persistedState struct {
	Stats     persistedStats         `json:"stats"`
	Trackings map[string]*cacheEntry `json:"trackings"`
}

// TrackingStore implementa cache L1 em memória (TTL dinâmico + cooldown anti-abuso),
// persistência L2 atômica em arquivo JSON (com fallback /tmp em ambiente Vercel serverless)
// e integração opcional com Vercel KV / Upstash Redis REST.
type TrackingStore struct {
	mu              sync.RWMutex
	filePath        string
	baseTTL         time.Duration
	refreshCooldown time.Duration
	items           map[string]*cacheEntry
	lastFetch       map[string]time.Time
	upstash         *upstashClient

	hits         atomic.Int64
	misses       atomic.Int64
	creditsSaved atomic.Int64
}

// NewTrackingStore cria e hidrata um TrackingStore a partir do disco (e Upstash REST, se configurado).
func NewTrackingStore(filePath string, cacheTTL time.Duration, refreshCooldown time.Duration) (*TrackingStore, error) {
	filePath = resolveStoreFilePath(filePath)
	if cacheTTL <= 0 {
		cacheTTL = DefaultCacheTTL
	}
	if refreshCooldown < 0 {
		refreshCooldown = DefaultRefreshCooldown
	}

	s := &TrackingStore{
		filePath:        filePath,
		baseTTL:         cacheTTL,
		refreshCooldown: refreshCooldown,
		items:           make(map[string]*cacheEntry),
		lastFetch:       make(map[string]time.Time),
		upstash:         newUpstashClientFromEnv(),
	}

	if err := s.loadFromDisk(); err != nil {
		return nil, err
	}
	return s, nil
}

// Get busca um rastreamento no cache L1 e, em caso de miss, consulta o Upstash Redis REST se configurado.
// Quando allowStale é false, registros expirados retornam cache miss para forçar revalidação.
func (s *TrackingStore) Get(code string, allowStale bool) (*domain.TrackingResult, bool) {
	norm := sanitizeCode(code)
	if norm == "" {
		s.misses.Add(1)
		return nil, false
	}

	s.mu.RLock()
	entry, ok := s.items[norm]
	if ok && entry != nil && entry.Result != nil {
		now := time.Now().UTC()
		expired := !entry.Result.CacheExpiresAt.IsZero() && now.After(entry.Result.CacheExpiresAt)
		if expired && !allowStale {
			s.mu.RUnlock()
			s.misses.Add(1)
			return nil, false
		}
		cloned := cloneTrackingResult(entry.Result)
		s.mu.RUnlock()

		cloned.Cached = true
		s.hits.Add(1)
		s.creditsSaved.Add(1)
		return cloned, true
	}
	s.mu.RUnlock()

	// Em cold starts serverless (ex: Vercel), hidrata a partir do Upstash REST / Vercel KV gratuito.
	if s.upstash != nil {
		if remoteEntry, found := s.upstash.getTracking(norm); found && remoteEntry != nil && remoteEntry.Result != nil {
			s.mu.Lock()
			s.items[norm] = remoteEntry
			fetchAt := remoteEntry.LastFetchAt
			if fetchAt.IsZero() {
				fetchAt = remoteEntry.Result.LastUpdatedAt
			}
			if !fetchAt.IsZero() {
				s.lastFetch[norm] = fetchAt
			}
			s.mu.Unlock()

			now := time.Now().UTC()
			expired := !remoteEntry.Result.CacheExpiresAt.IsZero() && now.After(remoteEntry.Result.CacheExpiresAt)
			if expired && !allowStale {
				s.misses.Add(1)
				return nil, false
			}
			cloned := cloneTrackingResult(remoteEntry.Result)
			cloned.Cached = true
			s.hits.Add(1)
			s.creditsSaved.Add(1)
			return cloned, true
		}
	}

	s.misses.Add(1)
	return nil, false
}

// CanRefresh verifica se o cooldown anti-abuso permite consultar os provedores externos para este código.
func (s *TrackingStore) CanRefresh(code string) (bool, time.Duration) {
	if s.refreshCooldown <= 0 {
		return true, 0
	}
	norm := sanitizeCode(code)
	if norm == "" {
		return true, 0
	}

	s.mu.RLock()
	last, ok := s.lastFetch[norm]
	s.mu.RUnlock()

	if (!ok || last.IsZero()) && s.upstash != nil {
		if remoteEntry, found := s.upstash.getTracking(norm); found && remoteEntry != nil {
			fetchAt := remoteEntry.LastFetchAt
			if fetchAt.IsZero() && remoteEntry.Result != nil {
				fetchAt = remoteEntry.Result.LastUpdatedAt
			}
			if !fetchAt.IsZero() {
				s.mu.Lock()
				s.items[norm] = remoteEntry
				s.lastFetch[norm] = fetchAt
				s.mu.Unlock()
				last = fetchAt
				ok = true
			}
		}
	}

	if !ok || last.IsZero() {
		return true, 0
	}

	elapsed := time.Since(last)
	if elapsed >= s.refreshCooldown {
		return true, 0
	}
	return false, s.refreshCooldown - elapsed
}

// Save grava o resultado no cache L1 (com TTL dinâmico por status) e persiste atomicamente no L2 em disco.
func (s *TrackingStore) Save(result *domain.TrackingResult) error {
	if result == nil {
		return errors.New("cannot save nil tracking result")
	}
	norm := sanitizeCode(result.TrackingCode)
	if norm == "" {
		return errors.New("trackingCode is required")
	}

	now := time.Now().UTC()
	toStore := cloneTrackingResult(result)
	toStore.TrackingCode = norm
	toStore.Cached = false

	if toStore.LastUpdatedAt.IsZero() {
		toStore.LastUpdatedAt = now
	}
	if toStore.CacheExpiresAt.IsZero() {
		toStore.CacheExpiresAt = toStore.LastUpdatedAt.Add(s.ttlForStatus(toStore.CurrentStatus))
	}
	if toStore.CurrentStatusLabel == "" {
		toStore.CurrentStatusLabel = domain.StatusLabel(toStore.CurrentStatus)
	}
	if toStore.ProgressStep == 0 {
		toStore.ProgressStep = progressStepForEvents(toStore.Events, toStore.CurrentStatus)
	}
	if toStore.RoutePoints == nil {
		toStore.RoutePoints = []domain.GeoPoint{}
	}
	if toStore.Events == nil {
		toStore.Events = []domain.TrackingEvent{}
	}

	// Reflete os timestamps normalizados no ponteiro original para o chamador retornar na resposta HTTP.
	result.TrackingCode = toStore.TrackingCode
	result.LastUpdatedAt = toStore.LastUpdatedAt
	result.CacheExpiresAt = toStore.CacheExpiresAt
	result.CurrentStatusLabel = toStore.CurrentStatusLabel
	result.ProgressStep = toStore.ProgressStep

	entry := &cacheEntry{
		Result:      toStore,
		LastFetchAt: now,
	}
	ttl := s.ttlForStatus(toStore.CurrentStatus)

	s.mu.Lock()
	s.items[norm] = entry
	s.lastFetch[norm] = now
	s.evictLocked(now)
	err := s.persistLocked()
	var recentSnapshot []domain.RecentTrackingSummary
	var entrySnapshot *cacheEntry
	if s.upstash != nil {
		if !isVercelEnv() {
			recentSnapshot = s.buildRecentLocked(upstashRecentMaxSize)
		}
		entrySnapshot = &cacheEntry{
			Result:      cloneTrackingResult(toStore),
			LastFetchAt: now,
		}
	}
	s.mu.Unlock()

	if s.upstash != nil && entrySnapshot != nil {
		s.upstash.saveTracking(norm, entrySnapshot, ttl, recentSnapshot)
	}

	return err
}

// UpsertFromWebhook mescla eventos recebidos via webhook no histórico existente,
// recalcula status atual e ProgressStep, atualiza L1 e persiste atomicamente em disco (e Upstash REST).
func (s *TrackingStore) UpsertFromWebhook(code string, carrierHint domain.CarrierInfo, rawEvents []domain.TrackingEvent) (*domain.TrackingResult, error) {
	norm := sanitizeCode(code)
	if norm == "" {
		return nil, errors.New("tracking code is required")
	}

	now := time.Now().UTC()

	// Em cold start serverless, tenta hidratar antes se ainda não estiver na L1.
	if s.upstash != nil {
		s.mu.RLock()
		_, existsL1 := s.items[norm]
		s.mu.RUnlock()
		if !existsL1 {
			if remoteEntry, found := s.upstash.getTracking(norm); found && remoteEntry != nil && remoteEntry.Result != nil {
				s.mu.Lock()
				if _, stillMissing := s.items[norm]; !stillMissing {
					s.items[norm] = remoteEntry
				}
				s.mu.Unlock()
			}
		}
	}

	s.mu.Lock()

	var current *domain.TrackingResult
	if existing, ok := s.items[norm]; ok && existing != nil && existing.Result != nil {
		current = cloneTrackingResult(existing.Result)
	} else {
		current = &domain.TrackingResult{
			TrackingCode: norm,
			Carrier:      carrierHint,
			RoutePoints:  []domain.GeoPoint{},
			Events:       []domain.TrackingEvent{},
		}
	}

	current.TrackingCode = norm
	current.Carrier = mergeCarrierInfo(current.Carrier, carrierHint)
	current.Events = mergeAndSortEvents(current.Events, rawEvents, now)

	if len(current.Events) > 0 {
		latest := current.Events[0]
		current.CurrentStatus = latest.StandardStatus
		if current.CurrentStatus == "" {
			current.CurrentStatus = domain.StatusInTransit
		}
		current.CurrentStatusLabel = domain.StatusLabel(current.CurrentStatus)
		current.ProgressStep = progressStepForEvents(current.Events, current.CurrentStatus)
	} else if current.CurrentStatus == "" {
		current.CurrentStatus = domain.StatusPosted
		current.CurrentStatusLabel = domain.StatusLabel(domain.StatusPosted)
		current.ProgressStep = 1
	}

	current.ProviderUsed = "webhook"
	current.Cached = false
	current.LastUpdatedAt = now
	ttl := s.ttlForStatus(current.CurrentStatus)
	current.CacheExpiresAt = now.Add(ttl)
	if current.RoutePoints == nil {
		current.RoutePoints = []domain.GeoPoint{}
	}

	storedEntry := &cacheEntry{
		Result:      cloneTrackingResult(current),
		LastFetchAt: now,
	}
	s.items[norm] = storedEntry
	s.lastFetch[norm] = now
	s.evictLocked(now)

	err := s.persistLocked()
	var recentSnapshot []domain.RecentTrackingSummary
	var entrySnapshot *cacheEntry
	if err == nil && s.upstash != nil {
		if !isVercelEnv() {
			recentSnapshot = s.buildRecentLocked(upstashRecentMaxSize)
		}
		entrySnapshot = &cacheEntry{
			Result:      cloneTrackingResult(current),
			LastFetchAt: now,
		}
	}
	s.mu.Unlock()

	if err != nil {
		return nil, err
	}
	if s.upstash != nil && entrySnapshot != nil {
		s.upstash.saveTracking(norm, entrySnapshot, ttl, recentSnapshot)
	}
	return current, nil
}

// Recent retorna os rastreamentos mais recentemente atualizados ordenados do mais novo para o mais antigo.
// Em ambiente Vercel (produção ou preview), retorna lista vazia para evitar exposição pública de códigos de terceiros (IDOR/PII).
func (s *TrackingStore) Recent(limit int) []domain.RecentTrackingSummary {
	if isVercelEnv() {
		return []domain.RecentTrackingSummary{}
	}
	if limit <= 0 {
		limit = 10
	}

	s.mu.RLock()
	items := s.buildRecentLocked(limit)
	s.mu.RUnlock()

	if len(items) == 0 && s.upstash != nil {
		if remoteRecent, ok := s.upstash.getRecent(limit); ok && len(remoteRecent) > 0 {
			return remoteRecent
		}
	}
	return items
}

func (s *TrackingStore) buildRecentLocked(limit int) []domain.RecentTrackingSummary {
	items := make([]domain.RecentTrackingSummary, 0, len(s.items))
	for _, entry := range s.items {
		if entry == nil || entry.Result == nil {
			continue
		}
		r := entry.Result
		var cddName string
		if r.CDD != nil {
			cddName = r.CDD.Name
		}
		items = append(items, domain.RecentTrackingSummary{
			TrackingCode:       r.TrackingCode,
			CarrierCode:        r.Carrier.Code,
			CarrierName:        r.Carrier.Name,
			CurrentStatus:      r.CurrentStatus,
			CurrentStatusLabel: r.CurrentStatusLabel,
			ProviderUsed:       r.ProviderUsed,
			LastUpdatedAt:      r.LastUpdatedAt,
			CDDName:            cddName,
		})
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].LastUpdatedAt.Equal(items[j].LastUpdatedAt) {
			return items[i].TrackingCode < items[j].TrackingCode
		}
		return items[i].LastUpdatedAt.After(items[j].LastUpdatedAt)
	})

	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items
}

// Stats retorna as métricas de cache e o modo de persistência ativo/configurado.
func (s *TrackingStore) Stats() domain.CacheStats {
	s.mu.RLock()
	stored := len(s.items)
	s.mu.RUnlock()

	return domain.CacheStats{
		Hits:            s.hits.Load(),
		Misses:          s.misses.Load(),
		StoredCount:     stored,
		CreditsSaved:    s.creditsSaved.Load(),
		BackendMode:     detectBackendMode(s.upstash != nil),
		CooldownSeconds: int(s.refreshCooldown.Seconds()),
	}
}

func (s *TrackingStore) ttlForStatus(status domain.StandardStatus) time.Duration {
	// Em execuções com TTL reduzido (ex: testes unitários < 1m), respeitar o baseTTL configurado.
	if s.baseTTL < time.Minute {
		return s.baseTTL
	}

	switch status {
	case domain.StatusDelivered:
		return 24 * time.Hour
	case domain.StatusAtCDD, domain.StatusOutForDelivery:
		if s.baseTTL < 10*time.Minute {
			return s.baseTTL
		}
		return 10 * time.Minute
	case domain.StatusPosted, domain.StatusInTransit, domain.StatusException:
		return s.baseTTL
	default:
		return s.baseTTL
	}
}

// CheckDistributedRateLimit consulta o Upstash Redis (quando configurado) para rate limit por IP entre instâncias serverless.
func (s *TrackingStore) CheckDistributedRateLimit(ip string, rpm int) (bool, bool) {
	if s == nil || s.upstash == nil || rpm <= 0 {
		return true, false
	}
	return s.upstash.checkRateLimit(ip, rpm, 60)
}

func (s *TrackingStore) evictLocked(now time.Time) {
	// 1. Limpar entradas antigas do mapa lastFetch.
	cooldownCutoff := s.refreshCooldown
	if cooldownCutoff < 10*time.Minute {
		cooldownCutoff = 10 * time.Minute
	}
	for k, ts := range s.lastFetch {
		if now.Sub(ts) > cooldownCutoff {
			delete(s.lastFetch, k)
		}
	}

	// 2. Remover itens expirados há mais de 2 horas (fora da janela de stale fallback).
	for k, entry := range s.items {
		if entry == nil || entry.Result == nil {
			delete(s.items, k)
			continue
		}
		if !entry.Result.CacheExpiresAt.IsZero() && now.After(entry.Result.CacheExpiresAt.Add(2*time.Hour)) {
			delete(s.items, k)
		}
	}

	// 3. Limitar capacidade máxima para evitar OOM ou esgotamento de /tmp.
	if len(s.items) <= maxStoreEntries {
		return
	}
	type agedKey struct {
		key string
		ts  time.Time
	}
	all := make([]agedKey, 0, len(s.items))
	for k, entry := range s.items {
		ts := entry.LastFetchAt
		if ts.IsZero() && entry.Result != nil {
			ts = entry.Result.LastUpdatedAt
		}
		all = append(all, agedKey{key: k, ts: ts})
	}
	sort.Slice(all, func(i, j int) bool {
		return all[i].ts.Before(all[j].ts)
	})
	excess := len(s.items) - maxStoreEntries
	for i := 0; i < excess && i < len(all); i++ {
		delete(s.items, all[i].key)
	}
}

func (s *TrackingStore) loadFromDisk() error {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read store file %s: %w", s.filePath, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}

	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		// Em ambiente Vercel serverless (/tmp efêmero), recupera de arquivo corrompido sem travar cold start.
		if isVercelEnv() {
			return nil
		}
		return fmt.Errorf("decode store file %s: %w", s.filePath, err)
	}

	s.hits.Store(state.Stats.Hits)
	s.misses.Store(state.Stats.Misses)
	s.creditsSaved.Store(state.Stats.CreditsSaved)

	for code, entry := range state.Trackings {
		if entry == nil || entry.Result == nil {
			continue
		}
		norm := sanitizeCode(code)
		if norm == "" {
			norm = sanitizeCode(entry.Result.TrackingCode)
		}
		if norm == "" {
			continue
		}
		entry.Result.TrackingCode = norm
		if entry.Result.RoutePoints == nil {
			entry.Result.RoutePoints = []domain.GeoPoint{}
		}
		if entry.Result.Events == nil {
			entry.Result.Events = []domain.TrackingEvent{}
		}
		s.items[norm] = entry

		fetchTime := entry.LastFetchAt
		if fetchTime.IsZero() {
			fetchTime = entry.Result.LastUpdatedAt
		}
		if !fetchTime.IsZero() {
			s.lastFetch[norm] = fetchTime
		}
	}
	return nil
}

func (s *TrackingStore) persistLocked() error {
	state := persistedState{
		Stats: persistedStats{
			Hits:         s.hits.Load(),
			Misses:       s.misses.Load(),
			CreditsSaved: s.creditsSaved.Load(),
		},
		Trackings: s.items,
	}

	payload, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal store state: %w", err)
	}

	if err := writeAtomicFile(s.filePath, payload); err != nil {
		tmpPath := defaultVercelTempPath()
		if s.filePath != tmpPath && isReadOnlyOrPermissionErr(err) {
			if tmpErr := writeAtomicFile(tmpPath, payload); tmpErr == nil {
				s.filePath = tmpPath
				return nil
			}
		}
		// Em runtime serverless (ex: Vercel), nunca derruba a requisição caso o FS esteja bloqueado.
		if isVercelEnv() {
			return nil
		}
		return err
	}
	return nil
}

func writeAtomicFile(targetPath string, payload []byte) error {
	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create store dir %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, "trackings-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp store file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp store file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp store file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp store file: %w", err)
	}
	if err := os.Rename(tmpName, targetPath); err != nil {
		return fmt.Errorf("atomic rename store file: %w", err)
	}
	cleanup = false
	return nil
}

func resolveStoreFilePath(filePath string) string {
	trimmed := strings.TrimSpace(filePath)
	if isVercelEnv() {
		if trimmed == "" || trimmed == DefaultFilePath || trimmed == "../data/trackings.json" {
			return defaultVercelTempPath()
		}
	}
	if trimmed == "" {
		return DefaultFilePath
	}
	return trimmed
}

func defaultVercelTempPath() string {
	return filepath.Join(os.TempDir(), DefaultVercelFileName)
}

func isVercelEnv() bool {
	return strings.TrimSpace(os.Getenv("VERCEL")) != "" || strings.TrimSpace(os.Getenv("VERCEL_ENV")) != ""
}

func isReadOnlyOrPermissionErr(err error) bool {
	return errors.Is(err, syscall.EROFS) || errors.Is(err, os.ErrPermission)
}

func detectBackendMode(upstashEnabled bool) string {
	hasPG := strings.TrimSpace(os.Getenv("DATABASE_URL")) != ""
	hasRedis := strings.TrimSpace(os.Getenv("REDIS_URL")) != ""
	onVercel := isVercelEnv()

	base := "memory+json"
	switch {
	case onVercel && upstashEnabled:
		base = "vercel-tmp+upstash-rest"
	case onVercel:
		base = "vercel-tmp"
	case upstashEnabled:
		base = "memory+json+upstash-rest"
	}

	switch {
	case hasPG && hasRedis:
		return base + " (postgres+redis config detected)"
	case hasPG:
		return base + " (postgres config detected)"
	case hasRedis:
		return base + " (redis config detected)"
	default:
		return base
	}
}

func sanitizeCode(code string) string {
	var b strings.Builder
	b.Grow(len(code))
	for _, r := range strings.ToUpper(strings.TrimSpace(code)) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func mergeCarrierInfo(existing, hint domain.CarrierInfo) domain.CarrierInfo {
	if existing.Code == "" || existing.Code == "unknown" || existing.Code == "auto" {
		if hint.Code != "" {
			return hint
		}
		return existing
	}
	if hint.Code != "" && hint.Code != "unknown" && hint.Code != "auto" && hint.Confidence > existing.Confidence {
		return hint
	}
	if existing.Name == "" && hint.Name != "" {
		existing.Name = hint.Name
	}
	if existing.Track17CarrierID == 0 && hint.Track17CarrierID != 0 {
		existing.Track17CarrierID = hint.Track17CarrierID
	}
	if existing.TrackingMoreCode == "" && hint.TrackingMoreCode != "" {
		existing.TrackingMoreCode = hint.TrackingMoreCode
	}
	return existing
}

func mergeAndSortEvents(existing, incoming []domain.TrackingEvent, fallbackNow time.Time) []domain.TrackingEvent {
	type keyedEvent struct {
		ev    domain.TrackingEvent
		order int
	}

	byKey := make(map[string]keyedEvent, len(existing)+len(incoming))
	seq := 0

	add := func(ev domain.TrackingEvent) {
		if ev.Timestamp.IsZero() {
			ev.Timestamp = fallbackNow
		}
		if ev.StandardStatus == "" {
			ev.StandardStatus = domain.StatusInTransit
		}
		if ev.StandardLabel == "" {
			ev.StandardLabel = domain.StatusLabel(ev.StandardStatus)
		}

		key := eventDedupeKey(ev)
		if prev, ok := byKey[key]; ok {
			merged := prev.ev
			if merged.Coordinates == nil && ev.Coordinates != nil {
				cp := *ev.Coordinates
				merged.Coordinates = &cp
			}
			if merged.Description == "" && ev.Description != "" {
				merged.Description = ev.Description
			}
			if merged.Location == "" && ev.Location != "" {
				merged.Location = ev.Location
			}
			if merged.RawStatus == "" && ev.RawStatus != "" {
				merged.RawStatus = ev.RawStatus
			}
			if merged.StandardStatus == domain.StatusInTransit && ev.StandardStatus != domain.StatusInTransit {
				merged.StandardStatus = ev.StandardStatus
				merged.StandardLabel = ev.StandardLabel
			}
			byKey[key] = keyedEvent{ev: merged, order: seq}
			seq++
			return
		}

		cp := ev
		if ev.Coordinates != nil {
			coord := *ev.Coordinates
			cp.Coordinates = &coord
		}
		byKey[key] = keyedEvent{ev: cp, order: seq}
		seq++
	}

	for _, ev := range existing {
		add(ev)
	}
	for _, ev := range incoming {
		add(ev)
	}

	list := make([]keyedEvent, 0, len(byKey))
	for _, item := range byKey {
		list = append(list, item)
	}

	// Mais recente primeiro (ordem decrescente por Timestamp; empate mantém o último inserido primeiro).
	sort.Slice(list, func(i, j int) bool {
		if !list[i].ev.Timestamp.Equal(list[j].ev.Timestamp) {
			return list[i].ev.Timestamp.After(list[j].ev.Timestamp)
		}
		return list[i].order > list[j].order
	})

	out := make([]domain.TrackingEvent, len(list))
	for i, item := range list {
		out[i] = item.ev
	}
	return out
}

func eventDedupeKey(ev domain.TrackingEvent) string {
	ts := ev.Timestamp.UTC().Truncate(time.Second).Format(time.RFC3339)
	raw := strings.ToLower(strings.TrimSpace(ev.RawStatus))
	loc := strings.ToLower(strings.TrimSpace(ev.Location))
	desc := strings.ToLower(strings.TrimSpace(ev.Description))
	return ts + "|" + raw + "|" + loc + "|" + desc
}

func progressStepForEvents(events []domain.TrackingEvent, current domain.StandardStatus) int {
	switch current {
	case domain.StatusPosted:
		return 1
	case domain.StatusInTransit:
		return 2
	case domain.StatusAtCDD, domain.StatusOutForDelivery:
		return 3
	case domain.StatusDelivered:
		return 4
	case domain.StatusException:
		maxStep := 2
		for _, ev := range events {
			if ev.StandardStatus != domain.StatusException {
				s := progressStepForEvents(nil, ev.StandardStatus)
				if s > maxStep {
					maxStep = s
				}
			}
		}
		return maxStep
	default:
		return 2
	}
}

func cloneTrackingResult(src *domain.TrackingResult) *domain.TrackingResult {
	if src == nil {
		return nil
	}
	cp := *src

	if len(src.Carrier.SampleCodes) > 0 {
		cp.Carrier.SampleCodes = append([]string(nil), src.Carrier.SampleCodes...)
	}
	if src.Origin != nil {
		pt := *src.Origin
		cp.Origin = &pt
	}
	if src.Destination != nil {
		pt := *src.Destination
		cp.Destination = &pt
	}
	if src.CurrentLocation != nil {
		pt := *src.CurrentLocation
		cp.CurrentLocation = &pt
	}
	if src.CDD != nil {
		cdd := *src.CDD
		cp.CDD = &cdd
	}
	if src.RoutePoints != nil {
		cp.RoutePoints = make([]domain.GeoPoint, len(src.RoutePoints))
		copy(cp.RoutePoints, src.RoutePoints)
	}
	if src.Events != nil {
		cp.Events = make([]domain.TrackingEvent, len(src.Events))
		for i, ev := range src.Events {
			cp.Events[i] = ev
			if ev.Coordinates != nil {
				coord := *ev.Coordinates
				cp.Events[i].Coordinates = &coord
			}
		}
	}
	return &cp
}
