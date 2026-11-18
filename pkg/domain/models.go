package domain

import "time"

// StandardStatus representa os estados fixos padronizados do sistema,
// traduzidos a partir das mensagens específicas de cada transportadora.
type StandardStatus string

const (
	StatusPosted         StandardStatus = "POSTED"
	StatusInTransit      StandardStatus = "IN_TRANSIT"
	StatusAtCDD          StandardStatus = "AT_CDD"
	StatusOutForDelivery StandardStatus = "OUT_FOR_DELIVERY"
	StatusDelivered      StandardStatus = "DELIVERED"
	StatusException      StandardStatus = "EXCEPTION"
)

// StatusLabel retorna o rótulo amigável em português para um StandardStatus.
func StatusLabel(status StandardStatus) string {
	switch status {
	case StatusPosted:
		return "Postado"
	case StatusInTransit:
		return "Em trânsito"
	case StatusAtCDD:
		return "Alocado no CDD / Aguardando retirada"
	case StatusOutForDelivery:
		return "Saiu para entrega"
	case StatusDelivered:
		return "Entregue"
	case StatusException:
		return "Ocorrência / Atenção"
	default:
		return "Em trânsito"
	}
}

// CarrierInfo descreve uma transportadora detectada via Regex, heurística ou API agregadora.
type CarrierInfo struct {
	Code               string   `json:"code"`
	Name               string   `json:"name"`
	Track17CarrierID   int      `json:"track17CarrierId"`
	TrackingMoreCode   string   `json:"trackingMoreCode"`
	DetectedBy         string   `json:"detectedBy"` // "regex", "aggregator", "heuristic"
	Confidence         float64  `json:"confidence"`
	PatternDescription string   `json:"patternDescription"`
	RegexPattern       string   `json:"regexPattern,omitempty"`
	SampleCodes        []string `json:"sampleCodes,omitempty"`
}

// GeoPoint representa um ponto geográfico na rota da encomenda para renderização no Leaflet.
type GeoPoint struct {
	Lat          float64 `json:"lat"`
	Lng          float64 `json:"lng"`
	City         string  `json:"city"`
	State        string  `json:"state"`
	Address      string  `json:"address"`
	ZipCode      string  `json:"zipCode"`
	FacilityType string  `json:"facilityType"` // "ORIGIN", "HUB", "CDD", "DESTINATION"
	FacilityName string  `json:"facilityName"`
}

// CDDAllocation contém detalhes do Centro de Distribuição Domiciliária onde o pacote
// está alocado ou passou por triagem final, habilitando busca presencial via GPS.
type CDDAllocation struct {
	Name               string  `json:"name"`
	CarrierName        string  `json:"carrierName"`
	Address            string  `json:"address"`
	City               string  `json:"city"`
	State              string  `json:"state"`
	ZipCode            string  `json:"zipCode"`
	Phone              string  `json:"phone"`
	OpeningHours       string  `json:"openingHours"`
	Lat                float64 `json:"lat"`
	Lng                float64 `json:"lng"`
	AvailableForPickup bool    `json:"availableForPickup"`
	Instructions       string  `json:"instructions"`
}

// TrackingEvent representa um evento individual do histórico da encomenda,
// preservando o texto bruto da transportadora e o estado padronizado.
type TrackingEvent struct {
	Timestamp      time.Time      `json:"timestamp"`
	RawStatus      string         `json:"rawStatus"`
	StandardStatus StandardStatus `json:"standardStatus"`
	StandardLabel  string         `json:"standardLabel"`
	Description    string         `json:"description"`
	Location       string         `json:"location"`
	Coordinates    *GeoPoint      `json:"coordinates,omitempty"`
}

// TrackingResult é o contrato completo retornado pela API para o frontend.
type TrackingResult struct {
	TrackingCode       string          `json:"trackingCode"`
	Carrier            CarrierInfo     `json:"carrier"`
	CurrentStatus      StandardStatus  `json:"currentStatus"`
	CurrentStatusLabel string          `json:"currentStatusLabel"`
	ProgressStep       int             `json:"progressStep"` // 1=Postado, 2=Em trânsito, 3=No CDD/Saiu p/ entrega, 4=Entregue
	EstimatedDelivery  string          `json:"estimatedDelivery"`
	Origin             *GeoPoint       `json:"origin,omitempty"`
	Destination        *GeoPoint       `json:"destination,omitempty"`
	CurrentLocation    *GeoPoint       `json:"currentLocation,omitempty"`
	CDD                *CDDAllocation  `json:"cdd,omitempty"`
	RoutePoints        []GeoPoint      `json:"routePoints"`
	Events             []TrackingEvent `json:"events"`
	ProviderUsed       string          `json:"providerUsed"` // "cache", "free_direct", "17track", "trackingmore", "smart_demo_fallback"
	Cached             bool            `json:"cached"`
	CacheExpiresAt     time.Time       `json:"cacheExpiresAt"`
	LastUpdatedAt      time.Time       `json:"lastUpdatedAt"`
}

// ProviderHealth descreve a disponibilidade, consumo de créditos e estado de circuit-breaker de um provedor.
type ProviderHealth struct {
	Name             string     `json:"name"`
	Enabled          bool       `json:"enabled"`
	Configured       bool       `json:"configured"`
	Available        bool       `json:"available"`
	CircuitOpen      bool       `json:"circuitOpen"`
	CircuitUntil     *time.Time `json:"circuitUntil,omitempty"`
	CreditsRemaining int        `json:"creditsRemaining"` // -1 para ilimitado/gratuito
	RequestsUsed     int64      `json:"requestsUsed"`
	Failures         int64      `json:"failures"`
	LastError        string     `json:"lastError,omitempty"`
	LastLatencyMs    int64      `json:"lastLatencyMs"`
	Mode             string     `json:"mode"` // "free", "api_key", "local_fallback"
}

// CacheStats armazena métricas de economia de créditos proporcionadas pelo cache e banco.
type CacheStats struct {
	Hits            int64  `json:"hits"`
	Misses          int64  `json:"misses"`
	StoredCount     int    `json:"storedCount"`
	CreditsSaved    int64  `json:"creditsSaved"`
	BackendMode     string `json:"backendMode"` // "memory+json", "redis+postgres", etc.
	CooldownSeconds int    `json:"cooldownSeconds"`
}

// RecentTrackingSummary resume um rastreamento salvo recentemente para exibição rápida.
type RecentTrackingSummary struct {
	TrackingCode       string         `json:"trackingCode"`
	CarrierCode        string         `json:"carrierCode"`
	CarrierName        string         `json:"carrierName"`
	CurrentStatus      StandardStatus `json:"currentStatus"`
	CurrentStatusLabel string         `json:"currentStatusLabel"`
	ProviderUsed       string         `json:"providerUsed"`
	LastUpdatedAt      time.Time      `json:"lastUpdatedAt"`
	CDDName            string         `json:"cddName,omitempty"`
}

// GeoEnricher enriquece um TrackingResult com coordenadas reais de rota e alocação de CDD.
type GeoEnricher interface {
	Enrich(result *TrackingResult)
}
