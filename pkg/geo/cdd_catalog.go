package geo

import (
	"math"
	"sort"
	"strings"
	"unicode"

	"rapid-screening/pkg/domain"
)

// CatalogEntry representa um Hub ou CDD (Centro de Distribuição Domiciliária) cadastrado no Brasil.
type CatalogEntry struct {
	ID           string
	Name         string
	CarrierCode  string
	CarrierName  string
	FacilityType string // "HUB" ou "CDD"
	Neighborhood string
	City         string
	State        string
	Address      string
	ZipCode      string
	Phone        string
	OpeningHours string
	Lat          float64
	Lng          float64
	Instructions string
	Aliases      []string
}

type cityPoint struct {
	City    string
	State   string
	Lat     float64
	Lng     float64
	Aliases []string
}

// CatalogEnricher implementa domain.GeoEnricher utilizando um catálogo real de CDDs e Hubs no Brasil.
type CatalogEnricher struct {
	entries    []CatalogEntry
	cityIndex  []cityPoint
	stateIndex map[string]cityPoint
}

// NewCatalogEnricher inicializa o enriquecedor geográfico com o catálogo brasileiro de CDDs e Hubs.
func NewCatalogEnricher() *CatalogEnricher {
	entries := buildBrazilianCatalog()
	cities := buildFallbackCities()
	states := buildFallbackStates()
	return &CatalogEnricher{
		entries:    entries,
		cityIndex:  cities,
		stateIndex: states,
	}
}

// Entries retorna uma cópia do catálogo completo de CDDs/Hubs (útil para inspeção e testes).
func (c *CatalogEnricher) Entries() []CatalogEntry {
	out := make([]CatalogEntry, len(c.entries))
	copy(out, c.entries)
	return out
}

// Enrich resolve coordenadas reais de eventos, origem, destino, localização atual,
// polilinha RoutePoints para o Leaflet e alocação de CDD para retirada presencial.
func (c *CatalogEnricher) Enrich(result *domain.TrackingResult) {
	if result == nil {
		return
	}

	carrierCode := normalizeCarrierCode(result.Carrier.Code, result.Carrier.Name)

	// 1. Enriquecer coordenadas de cada evento a partir do texto Location/Description.
	for i := range result.Events {
		ev := &result.Events[i]
		defaultFacType := facilityTypeForEvent(*ev)

		if ev.Coordinates == nil || !hasValidCoords(ev.Coordinates) {
			queryLoc := ev.Location
			if ev.Coordinates != nil {
				queryLoc = joinNonEmpty(ev.Coordinates.FacilityName, ev.Coordinates.Address, ev.Coordinates.City, ev.Coordinates.State, ev.Location)
			}
			if pt := c.ResolveLocation(queryLoc, ev.Description, carrierCode, defaultFacType); pt != nil {
				if ev.Coordinates != nil {
					mergeGeoPoint(ev.Coordinates, pt, defaultFacType)
				} else {
					ev.Coordinates = pt
				}
			}
		} else {
			if ev.Coordinates.FacilityType == "" {
				ev.Coordinates.FacilityType = defaultFacType
			}
			if ev.Coordinates.City == "" || ev.Coordinates.State == "" || ev.Coordinates.FacilityName == "" {
				if pt := c.ResolveLocation(ev.Location, ev.Description, carrierCode, ev.Coordinates.FacilityType); pt != nil {
					if ev.Coordinates.City == "" {
						ev.Coordinates.City = pt.City
					}
					if ev.Coordinates.State == "" {
						ev.Coordinates.State = pt.State
					}
					if ev.Coordinates.Address == "" {
						ev.Coordinates.Address = pt.Address
					}
					if ev.Coordinates.ZipCode == "" {
						ev.Coordinates.ZipCode = pt.ZipCode
					}
					if ev.Coordinates.FacilityName == "" {
						ev.Coordinates.FacilityName = pt.FacilityName
					}
				}
			}
		}
	}

	// Ordenar uma cópia dos eventos em ordem cronológica (mais antigo -> mais recente) para construir a rota.
	chrono := chronologicalEvents(result.Events)

	// 2. Origem: primeiro evento cronológico com coordenadas ou Origin existente.
	if result.Origin != nil && !hasValidCoords(result.Origin) {
		if pt := c.ResolveLocation(joinNonEmpty(result.Origin.City, result.Origin.State, result.Origin.FacilityName), "", carrierCode, "ORIGIN"); pt != nil {
			mergeGeoPoint(result.Origin, pt, "ORIGIN")
		}
	}
	if result.Origin == nil || !hasValidCoords(result.Origin) {
		for _, ev := range chrono {
			if hasValidCoords(ev.Coordinates) {
				cp := cloneGeoPoint(ev.Coordinates)
				cp.FacilityType = "ORIGIN"
				result.Origin = cp
				break
			}
		}
	}

	// 3. Localização atual: evento cronológico mais recente com coordenadas.
	if result.CurrentLocation != nil && !hasValidCoords(result.CurrentLocation) {
		if pt := c.ResolveLocation(joinNonEmpty(result.CurrentLocation.City, result.CurrentLocation.State, result.CurrentLocation.FacilityName), "", carrierCode, "HUB"); pt != nil {
			mergeGeoPoint(result.CurrentLocation, pt, pt.FacilityType)
		}
	}
	if result.CurrentLocation == nil || !hasValidCoords(result.CurrentLocation) {
		for i := len(chrono) - 1; i >= 0; i-- {
			if hasValidCoords(chrono[i].Coordinates) {
				cp := cloneGeoPoint(chrono[i].Coordinates)
				result.CurrentLocation = cp
				break
			}
		}
	}

	// 4. Destino: resolver caso já tenha cidade/estado sem lat/lng ou se já entregue.
	if result.Destination != nil && !hasValidCoords(result.Destination) {
		locText := joinNonEmpty(result.Destination.Address, result.Destination.City, result.Destination.State, result.Destination.FacilityName)
		if pt := c.ResolveLocation(locText, "", carrierCode, "DESTINATION"); pt != nil {
			mergeGeoPoint(result.Destination, pt, "DESTINATION")
		}
	}
	if (result.Destination == nil || !hasValidCoords(result.Destination)) && result.CurrentStatus == domain.StatusDelivered {
		for i := len(chrono) - 1; i >= 0; i-- {
			if hasValidCoords(chrono[i].Coordinates) {
				cp := cloneGeoPoint(chrono[i].Coordinates)
				cp.FacilityType = "DESTINATION"
				result.Destination = cp
				break
			}
		}
	}

	// 5. Resolver/Completar CDD (quando houver evento de CDD, AT_CDD, OUT_FOR_DELIVERY, ou cidade destino/atual conhecida).
	c.enrichCDD(result, chrono, carrierCode)

	// Se Destination ainda estiver vazio e temos CDD conhecido, derivar cidade/estado de destino do CDD se status final ou em entrega.
	if result.Destination == nil && result.CDD != nil && result.CDD.City != "" {
		if result.CurrentStatus == domain.StatusAtCDD || result.CurrentStatus == domain.StatusOutForDelivery || result.CurrentStatus == domain.StatusDelivered {
			result.Destination = &domain.GeoPoint{
				Lat:          result.CDD.Lat,
				Lng:          result.CDD.Lng,
				City:         result.CDD.City,
				State:        result.CDD.State,
				Address:      result.CDD.Address,
				ZipCode:      result.CDD.ZipCode,
				FacilityType: "DESTINATION",
				FacilityName: result.CDD.City + "/" + result.CDD.State,
			}
		}
	}

	// Garantir CurrentLocation mesmo se eventos iniciais não tinham coordenadas no catálogo,
	// extraindo do CDD, Origin ou do texto/metadados do último evento registrado.
	if result.CurrentLocation == nil || !hasValidCoords(result.CurrentLocation) {
		if result.CDD != nil && result.CDD.Lat != 0 {
			result.CurrentLocation = &domain.GeoPoint{
				Lat:          result.CDD.Lat,
				Lng:          result.CDD.Lng,
				City:         result.CDD.City,
				State:        result.CDD.State,
				Address:      result.CDD.Address,
				ZipCode:      result.CDD.ZipCode,
				FacilityType: "CDD",
				FacilityName: result.CDD.Name,
			}
		} else if result.Origin != nil && hasValidCoords(result.Origin) {
			result.CurrentLocation = cloneGeoPoint(result.Origin)
		} else if len(chrono) > 0 {
			latestEv := chrono[len(chrono)-1]
			if latestEv.Coordinates != nil && (latestEv.Coordinates.City != "" || latestEv.Coordinates.FacilityName != "") {
				result.CurrentLocation = cloneGeoPoint(latestEv.Coordinates)
			} else if strings.TrimSpace(latestEv.Location) != "" {
				result.CurrentLocation = &domain.GeoPoint{
					City:         strings.TrimSpace(latestEv.Location),
					State:        extractStateCode(latestEv.Location + " " + latestEv.Description),
					FacilityType: facilityTypeForEvent(latestEv),
					FacilityName: strings.TrimSpace(latestEv.Location),
				}
			}
		}
	}

	// 6. Construir RoutePoints ordenada e deduplicada para polilinha no Leaflet.
	result.RoutePoints = buildRoutePoints(result, chrono)
}

// ResolveLocation tenta resolver um texto de localização (e descrição opcional) em um GeoPoint real no Brasil.
func (c *CatalogEnricher) ResolveLocation(location, description, carrierCode, facilityType string) *domain.GeoPoint {
	combined := strings.TrimSpace(location)
	if combined == "" {
		combined = strings.TrimSpace(description)
	}
	if combined == "" {
		return nil
	}

	// Tentar primeiro com o texto explícito de Location; se não resolver, incluir Description.
	entry := c.matchBestCatalogEntry(location, carrierCode, false)
	if entry == nil && description != "" {
		entry = c.matchBestCatalogEntry(description, carrierCode, false)
	}
	if entry != nil {
		ft := facilityType
		if ft == "" {
			ft = entry.FacilityType
		}
		return &domain.GeoPoint{
			Lat:          entry.Lat,
			Lng:          entry.Lng,
			City:         entry.City,
			State:        entry.State,
			Address:      entry.Address,
			ZipCode:      entry.ZipCode,
			FacilityType: ft,
			FacilityName: entry.Name,
		}
	}

	// Tentar cidades secundárias brasileiras conhecidas (ex: Cajamar, Guarulhos, Barueri, Florianópolis, etc.).
	if cp, ok := c.matchFallbackCity(location); ok {
		return cityToGeoPoint(cp, facilityType)
	}
	if description != "" {
		if cp, ok := c.matchFallbackCity(description); ok {
			return cityToGeoPoint(cp, facilityType)
		}
	}

	// Fallback por sigla UF (ex: "Interior - MG" -> capital do estado)
	if st := extractStateCode(location); st != "" {
		if cp, ok := c.stateIndex[st]; ok {
			return cityToGeoPoint(cp, facilityType)
		}
	}
	if description != "" {
		if st := extractStateCode(description); st != "" {
			if cp, ok := c.stateIndex[st]; ok {
				return cityToGeoPoint(cp, facilityType)
			}
		}
	}

	return nil
}

func (c *CatalogEnricher) enrichCDD(result *domain.TrackingResult, chrono []domain.TrackingEvent, carrierCode string) {
	carrierName := result.Carrier.Name
	if carrierName == "" {
		carrierName = displayCarrierName(carrierCode)
	}

	// Se o resultado já trouxe um CDD parcial, enriquecer com os dados completos do catálogo.
	if result.CDD != nil {
		locQuery := joinNonEmpty(result.CDD.Name, result.CDD.Address, result.CDD.City, result.CDD.State)
		if entry := c.matchBestCatalogEntry(locQuery, carrierCode, true); entry != nil {
			fillMissingCDDFields(result.CDD, entry, carrierName)
		} else if result.CDD.Lat != 0 && result.CDD.Lng != 0 {
			if nearest := c.findNearestEntry(result.CDD.Lat, result.CDD.Lng, carrierCode); nearest != nil {
				fillMissingCDDFields(result.CDD, nearest, carrierName)
			}
		}
		applyCDDStatusDetails(result.CDD, result.CurrentStatus)
		return
	}

	// Verificar se há motivo para alocar CDD:
	// - Status atual AT_CDD ou OUT_FOR_DELIVERY
	// - Evento com status AT_CDD, OUT_FOR_DELIVERY ou menção explícita a CDD/CEE/Unidade/Agência/Retirada
	// - Cidade de destino conhecida ou localização atual em uma cidade do catálogo
	var cddCandidateText string
	var cddCandidateCoords *domain.GeoPoint
	hasCDDSignal := result.CurrentStatus == domain.StatusAtCDD || result.CurrentStatus == domain.StatusOutForDelivery

	for i := len(chrono) - 1; i >= 0; i-- {
		ev := chrono[i]
		normText := normalizeText(ev.Location + " " + ev.Description + " " + ev.RawStatus)
		isCDDEv := ev.StandardStatus == domain.StatusAtCDD ||
			ev.StandardStatus == domain.StatusOutForDelivery ||
			containsAny(normText, "cdd", "cee ", "unidade de distribuicao", "unidade operacional", "agencia", "retirada", "base ")
		if isCDDEv {
			hasCDDSignal = true
			cddCandidateText = joinNonEmpty(ev.Location, ev.Description)
			cddCandidateCoords = ev.Coordinates
			break
		}
	}

	var matchedEntry *CatalogEntry

	// Prioridade 1: texto do evento de CDD.
	if cddCandidateText != "" {
		matchedEntry = c.matchBestCatalogEntry(cddCandidateText, carrierCode, true)
	}

	// Prioridade 2: Destination conhecida.
	if matchedEntry == nil && result.Destination != nil {
		destQuery := joinNonEmpty(result.Destination.FacilityName, result.Destination.City, result.Destination.State, result.Destination.Address)
		if destQuery != "" {
			matchedEntry = c.matchBestCatalogEntry(destQuery, carrierCode, true)
		}
		if matchedEntry == nil && hasValidCoords(result.Destination) && hasCDDSignal {
			matchedEntry = c.findNearestEntry(result.Destination.Lat, result.Destination.Lng, carrierCode)
		}
	}

	// Prioridade 3: Cidade atual ou evento mais recente conhecido.
	if matchedEntry == nil && result.CurrentLocation != nil {
		curQuery := joinNonEmpty(result.CurrentLocation.FacilityName, result.CurrentLocation.City, result.CurrentLocation.State)
		if curQuery != "" {
			matchedEntry = c.matchBestCatalogEntry(curQuery, carrierCode, true)
		}
		if matchedEntry == nil && hasValidCoords(result.CurrentLocation) && hasCDDSignal {
			matchedEntry = c.findNearestEntry(result.CurrentLocation.Lat, result.CurrentLocation.Lng, carrierCode)
		}
	}

	// Prioridade 4: Coordenadas do evento candidato a CDD.
	if matchedEntry == nil && hasValidCoords(cddCandidateCoords) {
		matchedEntry = c.findNearestEntry(cddCandidateCoords.Lat, cddCandidateCoords.Lng, carrierCode)
	}

	if matchedEntry == nil {
		return
	}

	// Só preencher CDD se houve sinal de CDD/entrega OU cidade destino/atual conhecida no catálogo.
	if !hasCDDSignal && result.Destination == nil && result.CurrentLocation == nil {
		return
	}

	alloc := entryToCDDAllocation(matchedEntry, carrierName)
	applyCDDStatusDetails(&alloc, result.CurrentStatus)
	result.CDD = &alloc
}

func (c *CatalogEnricher) matchBestCatalogEntry(text, carrierCode string, preferCDD bool) *CatalogEntry {
	norm := normalizeText(text)
	if norm == "" {
		return nil
	}
	stateHint := extractStateCode(text)

	var best *CatalogEntry
	bestScore := 0

	for i := range c.entries {
		e := &c.entries[i]
		score := 0

		// Correspondência por bairro/unidade específica (peso maior).
		matchedSpecific := false
		neighNorm := normalizeText(e.Neighborhood)
		if neighNorm != "" && containsWordOrPhrase(norm, neighNorm) {
			score += 60
			matchedSpecific = true
		}

		// Correspondência por nome da cidade.
		cityNorm := normalizeText(e.City)
		if cityNorm != "" && containsWordOrPhrase(norm, cityNorm) {
			score += 40
		}

		// Correspondência por alias específico (evitando contagem dupla de cidade/bairro puro).
		for _, alias := range e.Aliases {
			a := normalizeText(alias)
			if a == "" || a == cityNorm || a == neighNorm {
				continue
			}
			if containsWordOrPhrase(norm, a) {
				if matchedSpecific {
					score += 20
				} else {
					score += 35
				}
				break
			}
		}

		// Se nem bairro, nem alias, nem cidade bateram, não é um match válido para este texto.
		if score == 0 {
			continue
		}

		// Validar UF se houver indicação no texto para evitar falsos positivos homônimos.
		if stateHint != "" {
			if strings.EqualFold(stateHint, e.State) {
				score += 15
			} else {
				score -= 30
			}
		}

		if score <= 0 {
			continue
		}

		// Afinidade por transportadora: prioridade forte quando o código coincide.
		if carrierCode != "" && strings.EqualFold(e.CarrierCode, carrierCode) {
			score += 50
		} else if e.CarrierCode == "correios" {
			// Correios possui capilaridade nacional de CDD como fallback preferencial.
			score += 2
		}

		// Preferência leve por CDD vs HUB quando aplicável.
		if preferCDD && e.FacilityType == "CDD" {
			score += 5
		} else if !preferCDD && e.FacilityType == "HUB" {
			score += 4
		}

		if score > bestScore {
			bestScore = score
			best = e
		}
	}

	if bestScore < 35 {
		return nil
	}
	return best
}

func (c *CatalogEnricher) matchFallbackCity(text string) (cityPoint, bool) {
	norm := normalizeText(text)
	if norm == "" {
		return cityPoint{}, false
	}
	stateHint := extractStateCode(text)

	var best cityPoint
	bestScore := 0
	for _, cp := range c.cityIndex {
		score := 0
		if containsWordOrPhrase(norm, normalizeText(cp.City)) {
			score += 40
		} else {
			for _, alias := range cp.Aliases {
				if containsWordOrPhrase(norm, normalizeText(alias)) {
					score += 35
					break
				}
			}
		}
		if score == 0 {
			continue
		}
		if stateHint != "" {
			if strings.EqualFold(stateHint, cp.State) {
				score += 15
			} else {
				score -= 25
			}
		}
		if score > bestScore {
			bestScore = score
			best = cp
		}
	}
	return best, bestScore >= 30
}

func (c *CatalogEnricher) findNearestEntry(lat, lng float64, carrierCode string) *CatalogEntry {
	var best *CatalogEntry
	bestMetric := math.MaxFloat64

	for i := range c.entries {
		e := &c.entries[i]
		dist := haversineKm(lat, lng, e.Lat, e.Lng)
		// Pequeno bônus de proximidade lógica para a mesma transportadora dentro da mesma região metropolitana.
		metric := dist
		if carrierCode != "" && strings.EqualFold(e.CarrierCode, carrierCode) {
			metric -= 8.0
		}
		if e.FacilityType == "CDD" {
			metric -= 1.0
		}
		if metric < bestMetric {
			bestMetric = metric
			best = e
		}
	}
	return best
}

func entryToCDDAllocation(e *CatalogEntry, carrierName string) domain.CDDAllocation {
	cName := e.CarrierName
	if carrierName != "" && !strings.EqualFold(carrierName, "Transportadora Desconhecida") && !strings.EqualFold(carrierName, "Desconhecida") {
		// Se o catálogo escolhido é de outro operador como fallback da mesma cidade, preservar o nome da transportadora real se útil,
		// ou manter o nome do ponto se corresponder.
		if strings.EqualFold(e.CarrierName, carrierName) {
			cName = e.CarrierName
		} else {
			cName = carrierName + " (Parceiro/Hub: " + e.CarrierName + ")"
		}
	}

	return domain.CDDAllocation{
		Name:         e.Name,
		CarrierName:  cName,
		Address:      e.Address,
		City:         e.City,
		State:        e.State,
		ZipCode:      e.ZipCode,
		Phone:        e.Phone,
		OpeningHours: e.OpeningHours,
		Lat:          e.Lat,
		Lng:          e.Lng,
		Instructions: e.Instructions,
	}
}

func fillMissingCDDFields(dst *domain.CDDAllocation, src *CatalogEntry, carrierName string) {
	if dst.Name == "" {
		dst.Name = src.Name
	}
	if dst.CarrierName == "" {
		if carrierName != "" {
			dst.CarrierName = carrierName
		} else {
			dst.CarrierName = src.CarrierName
		}
	}
	if dst.Address == "" {
		dst.Address = src.Address
	}
	if dst.City == "" {
		dst.City = src.City
	}
	if dst.State == "" {
		dst.State = src.State
	}
	if dst.ZipCode == "" {
		dst.ZipCode = src.ZipCode
	}
	if dst.Phone == "" {
		dst.Phone = src.Phone
	}
	if dst.OpeningHours == "" {
		dst.OpeningHours = src.OpeningHours
	}
	if dst.Lat == 0 && dst.Lng == 0 {
		dst.Lat = src.Lat
		dst.Lng = src.Lng
	}
	if dst.Instructions == "" {
		dst.Instructions = src.Instructions
	}
}

func applyCDDStatusDetails(cdd *domain.CDDAllocation, status domain.StandardStatus) {
	if cdd == nil {
		return
	}
	switch status {
	case domain.StatusAtCDD:
		cdd.AvailableForPickup = true
		if cdd.Instructions == "" || !strings.Contains(strings.ToLower(cdd.Instructions), "disponível para retirada") {
			cdd.Instructions = "Objeto disponível para retirada presencial no CDD. Apresente documento oficial com foto e o código de rastreio em " + cdd.Address + " (" + cdd.OpeningHours + ")."
		}
	case domain.StatusOutForDelivery:
		cdd.AvailableForPickup = false
		cdd.Instructions = "Objeto saiu deste CDD para entrega ao destinatário. Caso não haja recebedor, poderá retornar para retirada em " + cdd.Address + " (" + cdd.OpeningHours + ")."
	case domain.StatusDelivered:
		cdd.AvailableForPickup = false
		cdd.Instructions = "Entrega concluída a partir desta unidade de distribuição (" + cdd.Address + ")."
	default:
		cdd.AvailableForPickup = false
		if cdd.Instructions == "" {
			cdd.Instructions = "CDD de referência mais próximo para triagem final e eventual retirada presencial em " + cdd.Address + " (" + cdd.OpeningHours + ")."
		}
	}
}

func buildRoutePoints(result *domain.TrackingResult, chrono []domain.TrackingEvent) []domain.GeoPoint {
	points := make([]domain.GeoPoint, 0, len(chrono)+3)

	appendUnique := func(pt *domain.GeoPoint) {
		if !hasValidCoords(pt) {
			return
		}
		if len(points) > 0 {
			last := points[len(points)-1]
			if haversineKm(last.Lat, last.Lng, pt.Lat, pt.Lng) < 0.05 && last.FacilityType == pt.FacilityType {
				return
			}
			// Evitar duplicar pontos exatamente na mesma coordenada consecutiva.
			if math.Abs(last.Lat-pt.Lat) < 0.0001 && math.Abs(last.Lng-pt.Lng) < 0.0001 {
				return
			}
		}
		points = append(points, *pt)
	}

	if result.Origin != nil {
		appendUnique(result.Origin)
	}
	for _, ev := range chrono {
		appendUnique(ev.Coordinates)
	}
	if result.CDD != nil && result.CDD.Lat != 0 && result.CDD.Lng != 0 {
		if result.CurrentStatus == domain.StatusAtCDD || result.CurrentStatus == domain.StatusOutForDelivery {
			appendUnique(&domain.GeoPoint{
				Lat:          result.CDD.Lat,
				Lng:          result.CDD.Lng,
				City:         result.CDD.City,
				State:        result.CDD.State,
				Address:      result.CDD.Address,
				ZipCode:      result.CDD.ZipCode,
				FacilityType: "CDD",
				FacilityName: result.CDD.Name,
			})
		}
	}
	if result.Destination != nil {
		appendUnique(result.Destination)
	}

	return points
}

func chronologicalEvents(events []domain.TrackingEvent) []domain.TrackingEvent {
	if len(events) == 0 {
		return nil
	}
	cp := make([]domain.TrackingEvent, len(events))
	copy(cp, events)

	// Verifica se há timestamps válidos para ordenar em ordem crescente.
	hasTimestamps := false
	for _, ev := range cp {
		if !ev.Timestamp.IsZero() {
			hasTimestamps = true
			break
		}
	}
	if !hasTimestamps {
		return cp
	}

	sort.SliceStable(cp, func(i, j int) bool {
		ti, tj := cp[i].Timestamp, cp[j].Timestamp
		if ti.IsZero() && tj.IsZero() {
			return false
		}
		if ti.IsZero() {
			return false
		}
		if tj.IsZero() {
			return true
		}
		return ti.Before(tj)
	})
	return cp
}

func facilityTypeForEvent(ev domain.TrackingEvent) string {
	switch ev.StandardStatus {
	case domain.StatusPosted:
		return "ORIGIN"
	case domain.StatusAtCDD, domain.StatusOutForDelivery:
		return "CDD"
	case domain.StatusDelivered:
		return "DESTINATION"
	}
	norm := normalizeText(ev.Location + " " + ev.Description + " " + ev.RawStatus)
	if containsAny(norm, "cdd", "cee ", "agencia", "retirada", "saiu para entrega") {
		return "CDD"
	}
	if containsAny(norm, "postado", "coletado") {
		return "ORIGIN"
	}
	if containsAny(norm, "entregue") {
		return "DESTINATION"
	}
	return "HUB"
}

func hasValidCoords(pt *domain.GeoPoint) bool {
	if pt == nil {
		return false
	}
	return pt.Lat != 0 || pt.Lng != 0
}

func cloneGeoPoint(pt *domain.GeoPoint) *domain.GeoPoint {
	if pt == nil {
		return nil
	}
	cp := *pt
	return &cp
}

func mergeGeoPoint(dst *domain.GeoPoint, src *domain.GeoPoint, defaultType string) {
	if dst == nil || src == nil {
		return
	}
	if dst.Lat == 0 && dst.Lng == 0 {
		dst.Lat = src.Lat
		dst.Lng = src.Lng
	}
	if dst.City == "" {
		dst.City = src.City
	}
	if dst.State == "" {
		dst.State = src.State
	}
	if dst.Address == "" {
		dst.Address = src.Address
	}
	if dst.ZipCode == "" {
		dst.ZipCode = src.ZipCode
	}
	if dst.FacilityType == "" {
		if defaultType != "" {
			dst.FacilityType = defaultType
		} else {
			dst.FacilityType = src.FacilityType
		}
	}
	if dst.FacilityName == "" {
		dst.FacilityName = src.FacilityName
	}
}

func cityToGeoPoint(cp cityPoint, facilityType string) *domain.GeoPoint {
	if facilityType == "" {
		facilityType = "HUB"
	}
	return &domain.GeoPoint{
		Lat:          cp.Lat,
		Lng:          cp.Lng,
		City:         cp.City,
		State:        cp.State,
		FacilityType: facilityType,
		FacilityName: cp.City + "/" + cp.State,
	}
}

func joinNonEmpty(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " ")
}

func normalizeCarrierCode(code, name string) string {
	norm := normalizeText(code + " " + name)
	switch {
	case strings.Contains(norm, "correios"):
		return "correios"
	case strings.Contains(norm, "loggi"):
		return "loggi"
	case strings.Contains(norm, "azul"):
		return "azul_cargo"
	case strings.Contains(norm, "mandae"):
		return "mandae"
	case strings.Contains(norm, "magalog"):
		return "magalog"
	case strings.Contains(norm, "jadlog"):
		return "jadlog"
	case strings.Contains(norm, "total"):
		return "total_express"
	default:
		return strings.ToLower(strings.TrimSpace(code))
	}
}

func displayCarrierName(code string) string {
	switch code {
	case "correios":
		return "Correios"
	case "loggi":
		return "Loggi"
	case "azul_cargo":
		return "Azul Cargo Express"
	case "mandae":
		return "Mandaê"
	case "magalog":
		return "Magalog"
	case "jadlog":
		return "Jadlog"
	case "total_express":
		return "Total Express"
	default:
		return "Transportadora"
	}
}

// normalizeText remove acentos comuns do português, converte para minúsculas e normaliza separadores.
func normalizeText(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	lastSpace := true
	for _, r := range strings.ToLower(s) {
		r = foldPortugueseRune(r)
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastSpace = false
		} else if !lastSpace {
			b.WriteByte(' ')
			lastSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}

func foldPortugueseRune(r rune) rune {
	switch r {
	case 'á', 'à', 'â', 'ã', 'ä':
		return 'a'
	case 'é', 'è', 'ê', 'ë':
		return 'e'
	case 'í', 'ì', 'î', 'ï':
		return 'i'
	case 'ó', 'ò', 'ô', 'õ', 'ö':
		return 'o'
	case 'ú', 'ù', 'û', 'ü':
		return 'u'
	case 'ç':
		return 'c'
	case 'ñ':
		return 'n'
	default:
		return r
	}
}

func containsWordOrPhrase(haystack, needle string) bool {
	if needle == "" || haystack == "" {
		return false
	}
	return strings.Contains(" "+haystack+" ", " "+needle+" ")
}

func containsAny(haystack string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}

var brazilianStates = map[string]bool{
	"AC": true, "AL": true, "AP": true, "AM": true, "BA": true, "CE": true,
	"DF": true, "ES": true, "GO": true, "MA": true, "MT": true, "MS": true,
	"MG": true, "PA": true, "PB": true, "PR": true, "PE": true, "PI": true,
	"RJ": true, "RN": true, "RS": true, "RO": true, "RR": true, "SC": true,
	"SP": true, "SE": true, "TO": true,
}

func extractStateCode(text string) string {
	if text == "" {
		return ""
	}
	tokens := strings.FieldsFunc(strings.ToUpper(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	// Percorrer de trás para frente porque UFs costumam aparecer no fim ("São Paulo - SP").
	for i := len(tokens) - 1; i >= 0; i-- {
		tok := tokens[i]
		if len(tok) == 2 && brazilianStates[tok] {
			// Evitar confundir preposições curtas "DE", "DO", "DA", "EM", "NO", "NA" (nenhuma é UF, mas "PA" ou "SE" ou "GO" ou "PI" ou "MA" ou "TO" podem requerer cuidado se minúsculas no original).
			if isLikelyStateToken(text, tok) {
				return tok
			}
		}
	}
	return ""
}

func isLikelyStateToken(rawText, upperTok string) bool {
	upper := strings.ToUpper(rawText)
	if strings.TrimSpace(upper) == upperTok {
		return true
	}
	if strings.Contains(upper, "/"+upperTok) ||
		strings.Contains(upper, "/ "+upperTok) ||
		strings.Contains(upper, "- "+upperTok) ||
		strings.Contains(upper, "-"+upperTok) ||
		strings.Contains(upper, ", "+upperTok) ||
		strings.Contains(upper, ","+upperTok) ||
		strings.Contains(upper, "("+upperTok+")") {
		return true
	}
	// Evitar confundir palavras minúsculas como "se" ou "para" com siglas de UF; exige maiúscula após espaço.
	return strings.Contains(rawText, " "+upperTok)
}

func haversineKm(lat1, lng1, lat2, lng2 float64) float64 {
	const earthRadiusKm = 6371.0
	dLat := (lat2 - lat1) * math.Pi / 180.0
	dLng := (lng2 - lng1) * math.Pi / 180.0
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180.0)*math.Cos(lat2*math.Pi/180.0)*
			math.Sin(dLng/2)*math.Sin(dLng/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusKm * c
}

func buildBrazilianCatalog() []CatalogEntry {
	return []CatalogEntry{
		// ==================== SÃO PAULO - VILA MARIANA ====================
		{
			ID:           "sp-vila-mariana-correios",
			Name:         "CDD / CEE Vila Mariana - Correios",
			CarrierCode:  "correios",
			CarrierName:  "Correios",
			FacilityType: "CDD",
			Neighborhood: "Vila Mariana",
			City:         "São Paulo",
			State:        "SP",
			Address:      "Rua Sena Madureira, 300 - Vila Mariana",
			ZipCode:      "04021-000",
			Phone:        "(11) 3003-0100",
			OpeningHours: "Seg a Sex 09:00–17:00 | Sáb 09:00–12:00",
			Lat:          -23.5882,
			Lng:          -46.6388,
			Instructions: "Retirada presencial no balcão do CEE Vila Mariana mediante apresentação de RG/CNH e código de rastreio.",
			Aliases:      []string{"cee vila mariana", "cdd vila mariana", "sena madureira", "zona sul sp"},
		},
		{
			ID:           "sp-vila-mariana-loggi",
			Name:         "Base Loggi Vila Mariana",
			CarrierCode:  "loggi",
			CarrierName:  "Loggi",
			FacilityType: "CDD",
			Neighborhood: "Vila Mariana",
			City:         "São Paulo",
			State:        "SP",
			Address:      "Av. Lins de Vasconcelos, 2410 - Vila Mariana",
			ZipCode:      "04112-000",
			Phone:        "(11) 4020-1460",
			OpeningHours: "Seg a Sáb 08:30–18:00",
			Lat:          -23.5915,
			Lng:          -46.6361,
			Instructions: "Ponto de apoio Loggi Vila Mariana. Confirme o token de retirada no app antes de comparecer.",
			Aliases:      []string{"loggi vila mariana", "lins de vasconcelos"},
		},
		{
			ID:           "sp-vila-mariana-mandae",
			Name:         "Hub Mandaê Vila Mariana",
			CarrierCode:  "mandae",
			CarrierName:  "Mandaê",
			FacilityType: "CDD",
			Neighborhood: "Vila Mariana",
			City:         "São Paulo",
			State:        "SP",
			Address:      "Rua Domingos de Morais, 1850 - Vila Mariana",
			ZipCode:      "04010-200",
			Phone:        "(11) 3198-9900",
			OpeningHours: "Seg a Sex 09:00–18:00",
			Lat:          -23.5848,
			Lng:          -46.6423,
			Instructions: "Central de triagem e retirada agendada Mandaê Zona Sul com documento oficial.",
			Aliases:      []string{"mandae vila mariana", "domingos de morais"},
		},

		// ==================== SÃO PAULO - PINHEIROS ====================
		{
			ID:           "sp-pinheiros-correios",
			Name:         "CDD Pinheiros - Correios",
			CarrierCode:  "correios",
			CarrierName:  "Correios",
			FacilityType: "CDD",
			Neighborhood: "Pinheiros",
			City:         "São Paulo",
			State:        "SP",
			Address:      "Rua dos Pinheiros, 870 - Pinheiros",
			ZipCode:      "05422-001",
			Phone:        "(11) 3003-0100",
			OpeningHours: "Seg a Sex 09:00–17:00 | Sáb 09:00–12:00",
			Lat:          -23.5667,
			Lng:          -46.6934,
			Instructions: "Retirada na unidade Pinheiros com documento original com foto e número do objeto.",
			Aliases:      []string{"cdd pinheiros", "ac pinheiros", "zona oeste sp"},
		},
		{
			ID:           "sp-pinheiros-loggi",
			Name:         "Hub Loggi Pinheiros",
			CarrierCode:  "loggi",
			CarrierName:  "Loggi",
			FacilityType: "CDD",
			Neighborhood: "Pinheiros",
			City:         "São Paulo",
			State:        "SP",
			Address:      "Rua Mateus Grou, 420 - Pinheiros",
			ZipCode:      "05415-040",
			Phone:        "(11) 4020-1460",
			OpeningHours: "Seg a Sáb 08:00–19:00",
			Lat:          -23.5629,
			Lng:          -46.6957,
			Instructions: "Base urbana Loggi Pinheiros para despacho last-mile e retirada mediante protocolo.",
			Aliases:      []string{"loggi pinheiros", "mateus grou"},
		},
		{
			ID:           "sp-pinheiros-mandae",
			Name:         "Hub Mandaê Pinheiros",
			CarrierCode:  "mandae",
			CarrierName:  "Mandaê",
			FacilityType: "CDD",
			Neighborhood: "Pinheiros",
			City:         "São Paulo",
			State:        "SP",
			Address:      "Av. Pedroso de Morais, 1120 - Pinheiros",
			ZipCode:      "05420-001",
			Phone:        "(11) 3198-9900",
			OpeningHours: "Seg a Sex 09:00–18:00",
			Lat:          -23.5692,
			Lng:          -46.6895,
			Instructions: "Unidade Mandaê Zona Oeste com retirada assistida no balcão.",
			Aliases:      []string{"mandae pinheiros", "pedroso de morais"},
		},

		// ==================== SÃO PAULO - MOOCA ====================
		{
			ID:           "sp-mooca-correios",
			Name:         "CDD Mooca - Correios",
			CarrierCode:  "correios",
			CarrierName:  "Correios",
			FacilityType: "CDD",
			Neighborhood: "Mooca",
			City:         "São Paulo",
			State:        "SP",
			Address:      "Rua da Mooca, 2540 - Mooca",
			ZipCode:      "03104-002",
			Phone:        "(11) 3003-0100",
			OpeningHours: "Seg a Sex 09:00–17:00 | Sáb 09:00–12:00",
			Lat:          -23.5535,
			Lng:          -46.5978,
			Instructions: "Retirada de encomendas da Zona Leste no CDD Mooca com documento oficial.",
			Aliases:      []string{"cdd mooca", "cee mooca", "zona leste sp"},
		},
		{
			ID:           "sp-mooca-jadlog",
			Name:         "Unidade Jadlog Mooca",
			CarrierCode:  "jadlog",
			CarrierName:  "Jadlog",
			FacilityType: "CDD",
			Neighborhood: "Mooca",
			City:         "São Paulo",
			State:        "SP",
			Address:      "Av. Paes de Barros, 1340 - Mooca",
			ZipCode:      "03114-000",
			Phone:        "(11) 3563-2000",
			OpeningHours: "Seg a Sex 08:30–18:00 | Sáb 08:30–12:00",
			Lat:          -23.5579,
			Lng:          -46.5944,
			Instructions: "Franquia Jadlog Mooca para coleta, entrega e retirada Pickup.",
			Aliases:      []string{"jadlog mooca", "paes de barros"},
		},
		{
			ID:           "sp-mooca-total",
			Name:         "Base Total Express Mooca",
			CarrierCode:  "total_express",
			CarrierName:  "Total Express",
			FacilityType: "CDD",
			Neighborhood: "Mooca",
			City:         "São Paulo",
			State:        "SP",
			Address:      "Rua Borges de Figueiredo, 680 - Mooca",
			ZipCode:      "03110-010",
			Phone:        "(11) 3627-5900",
			OpeningHours: "Seg a Sex 08:30–17:30",
			Lat:          -23.5498,
			Lng:          -46.6015,
			Instructions: "Base de distribuição last-mile Total Express Zona Leste.",
			Aliases:      []string{"total express mooca", "borges de figueiredo"},
		},

		// ==================== SÃO PAULO - BARRA FUNDA / HUBS SP ====================
		{
			ID:           "sp-barra-funda-correios",
			Name:         "Hub / CDD Barra Funda - Correios",
			CarrierCode:  "correios",
			CarrierName:  "Correios",
			FacilityType: "HUB",
			Neighborhood: "Barra Funda",
			City:         "São Paulo",
			State:        "SP",
			Address:      "Av. Marquês de São Vicente, 1250 - Barra Funda",
			ZipCode:      "01139-002",
			Phone:        "(11) 3003-0100",
			OpeningHours: "Seg a Sex 08:30–17:30 | Sáb 09:00–12:00",
			Lat:          -23.5256,
			Lng:          -46.6675,
			Instructions: "Centro de triagem e entrega Barra Funda; retirada autorizada quando status indicar aguardando retirada.",
			Aliases:      []string{"barra funda", "cte jaguare", "ctce sao paulo", "marques de sao vicente"},
		},
		{
			ID:           "sp-barra-funda-total",
			Name:         "CD Total Express Barra Funda",
			CarrierCode:  "total_express",
			CarrierName:  "Total Express",
			FacilityType: "HUB",
			Neighborhood: "Barra Funda",
			City:         "São Paulo",
			State:        "SP",
			Address:      "Rua Tagipuru, 450 - Barra Funda",
			ZipCode:      "01156-000",
			Phone:        "(11) 3627-5900",
			OpeningHours: "Seg a Sex 08:00–18:00",
			Lat:          -23.5218,
			Lng:          -46.6619,
			Instructions: "Hub metropolitano Total Express São Paulo; retirada presencial mediante protocolo de atendimento.",
			Aliases:      []string{"total barra funda", "tagipuru"},
		},
		{
			ID:           "sp-barra-funda-magalog",
			Name:         "Hub Magalog Barra Funda",
			CarrierCode:  "magalog",
			CarrierName:  "Magalog",
			FacilityType: "HUB",
			Neighborhood: "Barra Funda",
			City:         "São Paulo",
			State:        "SP",
			Address:      "Av. Nicolas Boer, 399 - Barra Funda",
			ZipCode:      "01140-060",
			Phone:        "(11) 3504-5500",
			OpeningHours: "Seg a Sex 08:00–18:00 | Sáb 08:00–13:00",
			Lat:          -23.5278,
			Lng:          -46.6721,
			Instructions: "Ponto de consolidação e expedição Magalog Capital; confirme disponibilidade no rastreio.",
			Aliases:      []string{"magalog barra funda", "magalog sao paulo", "nicolas boer"},
		},
		{
			ID:           "sp-barra-funda-azul",
			Name:         "Loja / CDD Azul Cargo Barra Funda",
			CarrierCode:  "azul_cargo",
			CarrierName:  "Azul Cargo Express",
			FacilityType: "CDD",
			Neighborhood: "Barra Funda",
			City:         "São Paulo",
			State:        "SP",
			Address:      "Av. Auro Soares de Moura Andrade, 664 - Barra Funda",
			ZipCode:      "01156-001",
			Phone:        "(11) 4003-8399",
			OpeningHours: "Seg a Sex 08:00–19:00 | Sáb 08:00–14:00",
			Lat:          -23.5241,
			Lng:          -46.6652,
			Instructions: "Retirada expressa Azul Cargo com AWB/código de rastreio e documento oficial.",
			Aliases:      []string{"azul barra funda", "azul sao paulo", "terminal barra funda"},
		},

		// ==================== CAMPINAS / VIRACOPOS ====================
		{
			ID:           "vcp-azul-hub",
			Name:         "Hub Azul Cargo Express - Aeroporto de Viracopos",
			CarrierCode:  "azul_cargo",
			CarrierName:  "Azul Cargo Express",
			FacilityType: "HUB",
			Neighborhood: "Viracopos",
			City:         "Campinas",
			State:        "SP",
			Address:      "Rodovia Santos Dumont, Km 66 - Terminal de Cargas Viracopos",
			ZipCode:      "13052-901",
			Phone:        "(19) 4003-8399",
			OpeningHours: "Seg a Sex 07:00–21:00 | Sáb 08:00–16:00",
			Lat:          -23.0074,
			Lng:          -47.1345,
			Instructions: "Terminal de Cargas Azul Cargo Viracopos (VCP). Apresente AWB e documento com foto no balcão de cargas.",
			Aliases:      []string{"viracopos", "vcp", "hub azul cargo", "aeroporto viracopos", "teca vcp"},
		},
		{
			ID:           "campinas-correios",
			Name:         "CDD Campinas Centro - Correios",
			CarrierCode:  "correios",
			CarrierName:  "Correios",
			FacilityType: "CDD",
			Neighborhood: "Centro",
			City:         "Campinas",
			State:        "SP",
			Address:      "Av. Francisco Glicério, 900 - Centro",
			ZipCode:      "13012-100",
			Phone:        "(19) 3003-0100",
			OpeningHours: "Seg a Sex 09:00–17:00 | Sáb 09:00–12:00",
			Lat:          -22.9099,
			Lng:          -47.0626,
			Instructions: "Retirada presencial na unidade Campinas Centro com documento oficial e código do objeto.",
			Aliases:      []string{"cte campinas", "cdd campinas", "campinas"},
		},
		{
			ID:           "campinas-jadlog",
			Name:         "Hub Jadlog Campinas",
			CarrierCode:  "jadlog",
			CarrierName:  "Jadlog",
			FacilityType: "CDD",
			Neighborhood: "Jardim Nova Europa",
			City:         "Campinas",
			State:        "SP",
			Address:      "Av. Baden Powell, 1140 - Jardim Nova Europa",
			ZipCode:      "13040-093",
			Phone:        "(19) 3563-2000",
			OpeningHours: "Seg a Sex 08:30–18:00 | Sáb 08:30–12:00",
			Lat:          -22.9182,
			Lng:          -47.0815,
			Instructions: "Unidade Jadlog Campinas para expedição e retirada de remessas.",
			Aliases:      []string{"jadlog campinas"},
		},
		{
			ID:           "campinas-total",
			Name:         "CD Total Express Campinas",
			CarrierCode:  "total_express",
			CarrierName:  "Total Express",
			FacilityType: "HUB",
			Neighborhood: "Distrito Industrial",
			City:         "Campinas",
			State:        "SP",
			Address:      "Av. Mercedes Benz, 460 - Distrito Industrial",
			ZipCode:      "13054-750",
			Phone:        "(19) 3627-5900",
			OpeningHours: "Seg a Sex 08:00–18:00",
			Lat:          -22.9270,
			Lng:          -47.0932,
			Instructions: "Cross-docking Total Express Interior Paulista.",
			Aliases:      []string{"total campinas"},
		},
		{
			ID:           "campinas-loggi",
			Name:         "Base Loggi Campinas",
			CarrierCode:  "loggi",
			CarrierName:  "Loggi",
			FacilityType: "CDD",
			Neighborhood: "Guanabara",
			City:         "Campinas",
			State:        "SP",
			Address:      "Av. Brasil, 920 - Jardim Guanabara",
			ZipCode:      "13073-012",
			Phone:        "(19) 4020-1460",
			OpeningHours: "Seg a Sáb 08:30–18:00",
			Lat:          -22.9012,
			Lng:          -47.0584,
			Instructions: "Base metropolitana Loggi Campinas.",
			Aliases:      []string{"loggi campinas"},
		},

		// ==================== RIO DE JANEIRO - BENFICA / CENTRO ====================
		{
			ID:           "rj-benfica-correios",
			Name:         "CTCE / CDD Benfica - Correios",
			CarrierCode:  "correios",
			CarrierName:  "Correios",
			FacilityType: "HUB",
			Neighborhood: "Benfica",
			City:         "Rio de Janeiro",
			State:        "RJ",
			Address:      "Rua José Clemente, 62 - Benfica",
			ZipCode:      "20930-970",
			Phone:        "(21) 3003-0100",
			OpeningHours: "Seg a Sex 09:00–17:00 | Sáb 09:00–12:00",
			Lat:          -22.8919,
			Lng:          -43.2435,
			Instructions: "Complexo Logístico Correios Benfica; retirada no setor de encomendas mediante documento com foto.",
			Aliases:      []string{"benfica", "ctce benfica", "cte benfica", "cdd benfica", "cte rio"},
		},
		{
			ID:           "rj-benfica-total",
			Name:         "Hub Total Express Benfica",
			CarrierCode:  "total_express",
			CarrierName:  "Total Express",
			FacilityType: "HUB",
			Neighborhood: "Benfica",
			City:         "Rio de Janeiro",
			State:        "RJ",
			Address:      "Rua Prefeito Olímpio de Melo, 1520 - Benfica",
			ZipCode:      "20930-001",
			Phone:        "(21) 3627-5900",
			OpeningHours: "Seg a Sex 08:00–18:00",
			Lat:          -22.8948,
			Lng:          -43.2412,
			Instructions: "Centro de distribuição Total Express Rio de Janeiro.",
			Aliases:      []string{"total express rj", "total benfica"},
		},
		{
			ID:           "rj-benfica-loggi",
			Name:         "Base Loggi Benfica",
			CarrierCode:  "loggi",
			CarrierName:  "Loggi",
			FacilityType: "CDD",
			Neighborhood: "Benfica",
			City:         "Rio de Janeiro",
			State:        "RJ",
			Address:      "Av. Dom Hélder Câmara, 410 - Benfica",
			ZipCode:      "20911-290",
			Phone:        "(21) 4020-1460",
			OpeningHours: "Seg a Sáb 08:30–18:30",
			Lat:          -22.8895,
			Lng:          -43.2467,
			Instructions: "Hub urbano Loggi Rio de Janeiro.",
			Aliases:      []string{"loggi benfica", "loggi rio"},
		},
		{
			ID:           "rj-centro-correios",
			Name:         "CDD / AC Central Rio de Janeiro - Correios",
			CarrierCode:  "correios",
			CarrierName:  "Correios",
			FacilityType: "CDD",
			Neighborhood: "Centro",
			City:         "Rio de Janeiro",
			State:        "RJ",
			Address:      "Rua Primeiro de Março, 64 - Centro",
			ZipCode:      "20010-974",
			Phone:        "(21) 3003-0100",
			OpeningHours: "Seg a Sex 09:00–17:00",
			Lat:          -22.9035,
			Lng:          -43.1795,
			Instructions: "Atendimento para retirada presencial no Centro do Rio com RG e código de rastreamento.",
			Aliases:      []string{"rio de janeiro", "centro rj", "primeiro de marco"},
		},
		{
			ID:           "rj-centro-azul",
			Name:         "Azul Cargo Express Rio Centro / SDU",
			CarrierCode:  "azul_cargo",
			CarrierName:  "Azul Cargo Express",
			FacilityType: "CDD",
			Neighborhood: "Centro",
			City:         "Rio de Janeiro",
			State:        "RJ",
			Address:      "Praça Senador Salgado Filho, s/n - Centro (Santos Dumont)",
			ZipCode:      "20021-340",
			Phone:        "(21) 4003-8399",
			OpeningHours: "Seg a Sex 07:00–20:00 | Sáb 08:00–15:00",
			Lat:          -22.9105,
			Lng:          -43.1631,
			Instructions: "Balcão Azul Cargo Aeroporto Santos Dumont (SDU) e Galeão (GIG) para retirada com documento e AWB.",
			Aliases:      []string{"sdu", "gig", "azul rio", "santos dumont", "galeao"},
		},
		{
			ID:           "rj-centro-jadlog",
			Name:         "Unidade Jadlog Rio Centro",
			CarrierCode:  "jadlog",
			CarrierName:  "Jadlog",
			FacilityType: "CDD",
			Neighborhood: "Centro",
			City:         "Rio de Janeiro",
			State:        "RJ",
			Address:      "Av. Presidente Vargas, 1140 - Centro",
			ZipCode:      "20071-002",
			Phone:        "(21) 3563-2000",
			OpeningHours: "Seg a Sex 08:30–18:00 | Sáb 08:30–12:00",
			Lat:          -22.9012,
			Lng:          -43.1848,
			Instructions: "Ponto Jadlog Rio de Janeiro Centro.",
			Aliases:      []string{"jadlog rio", "jadlog rj"},
		},

		// ==================== BELO HORIZONTE - CONTAGEM / SAVASSI ====================
		{
			ID:           "bh-contagem-correios",
			Name:         "CTCE / CDD Contagem - Correios",
			CarrierCode:  "correios",
			CarrierName:  "Correios",
			FacilityType: "HUB",
			Neighborhood: "Contagem",
			City:         "Contagem",
			State:        "MG",
			Address:      "Av. Babita Camargos, 1295 - Cidade Industrial",
			ZipCode:      "32210-180",
			Phone:        "(31) 3003-0100",
			OpeningHours: "Seg a Sex 09:00–17:00 | Sáb 09:00–12:00",
			Lat:          -19.9388,
			Lng:          -44.0419,
			Instructions: "Centro de tratamento e distribuição metropolitana de Belo Horizonte em Contagem.",
			Aliases:      []string{"contagem", "ctce bh", "cte belo horizonte", "cidade industrial"},
		},
		{
			ID:           "bh-contagem-total",
			Name:         "Hub Total Express Contagem",
			CarrierCode:  "total_express",
			CarrierName:  "Total Express",
			FacilityType: "HUB",
			Neighborhood: "Contagem",
			City:         "Contagem",
			State:        "MG",
			Address:      "Via Expressa de Contagem, 3100 - Cinco",
			ZipCode:      "32370-485",
			Phone:        "(31) 3627-5900",
			OpeningHours: "Seg a Sex 08:00–18:00",
			Lat:          -19.9412,
			Lng:          -44.0468,
			Instructions: "Hub Minas Gerais Total Express; retirada mediante agendamento prévio.",
			Aliases:      []string{"total contagem", "total bh"},
		},
		{
			ID:           "bh-contagem-magalog",
			Name:         "Hub Magalog Contagem / RMBH",
			CarrierCode:  "magalog",
			CarrierName:  "Magalog",
			FacilityType: "HUB",
			Neighborhood: "Contagem",
			City:         "Contagem",
			State:        "MG",
			Address:      "Av. General David Sarnoff, 2400 - Cidade Industrial",
			ZipCode:      "32210-110",
			Phone:        "(31) 3504-5500",
			OpeningHours: "Seg a Sex 08:00–18:00",
			Lat:          -19.9345,
			Lng:          -44.0382,
			Instructions: "Centro de distribuição regional Magalog Minas Gerais.",
			Aliases:      []string{"magalog contagem", "magalog bh"},
		},
		{
			ID:           "bh-contagem-jadlog",
			Name:         "Unidade Jadlog Contagem / BH",
			CarrierCode:  "jadlog",
			CarrierName:  "Jadlog",
			FacilityType: "CDD",
			Neighborhood: "Contagem",
			City:         "Contagem",
			State:        "MG",
			Address:      "Av. Cardeal Eugênio Pacelli, 1980 - Cidade Industrial",
			ZipCode:      "32210-003",
			Phone:        "(31) 3563-2000",
			OpeningHours: "Seg a Sex 08:30–18:00 | Sáb 08:30–12:00",
			Lat:          -19.9437,
			Lng:          -44.0351,
			Instructions: "Filial Jadlog Grande BH para recebimento e retirada Pickup.",
			Aliases:      []string{"jadlog contagem", "jadlog bh"},
		},
		{
			ID:           "bh-savassi-correios",
			Name:         "CDD Savassi - Correios",
			CarrierCode:  "correios",
			CarrierName:  "Correios",
			FacilityType: "CDD",
			Neighborhood: "Savassi",
			City:         "Belo Horizonte",
			State:        "MG",
			Address:      "Rua Pernambuco, 1140 - Savassi",
			ZipCode:      "30130-151",
			Phone:        "(31) 3003-0100",
			OpeningHours: "Seg a Sex 09:00–17:00 | Sáb 09:00–12:00",
			Lat:          -19.9367,
			Lng:          -43.9344,
			Instructions: "CDD Centro-Sul BH (Savassi). Apresente documento oficial com foto para retirada.",
			Aliases:      []string{"savassi", "belo horizonte", "bh", "cdd savassi"},
		},
		{
			ID:           "bh-savassi-loggi",
			Name:         "Base Loggi Savassi / BH",
			CarrierCode:  "loggi",
			CarrierName:  "Loggi",
			FacilityType: "CDD",
			Neighborhood: "Savassi",
			City:         "Belo Horizonte",
			State:        "MG",
			Address:      "Av. Cristóvão Colombo, 519 - Savassi",
			ZipCode:      "30140-140",
			Phone:        "(31) 4020-1460",
			OpeningHours: "Seg a Sáb 08:30–18:00",
			Lat:          -19.9349,
			Lng:          -43.9321,
			Instructions: "Ponto avançado Loggi Belo Horizonte.",
			Aliases:      []string{"loggi savassi", "loggi bh"},
		},
		{
			ID:           "bh-azul-cargo",
			Name:         "Azul Cargo Express Belo Horizonte",
			CarrierCode:  "azul_cargo",
			CarrierName:  "Azul Cargo Express",
			FacilityType: "CDD",
			Neighborhood: "Centro",
			City:         "Belo Horizonte",
			State:        "MG",
			Address:      "Av. Olegário Maciel, 1600 - Lourdes / Centro",
			ZipCode:      "30180-111",
			Phone:        "(31) 4003-8399",
			OpeningHours: "Seg a Sex 08:00–19:00 | Sáb 08:00–13:00",
			Lat:          -19.9227,
			Lng:          -43.9451,
			Instructions: "Loja urbana Azul Cargo BH (integrada ao Hub Confins CNF).",
			Aliases:      []string{"cnf", "confins", "azul bh", "azul belo horizonte"},
		},

		// ==================== CURITIBA - CAJURU / CENTRO ====================
		{
			ID:           "cwb-cajuru-correios",
			Name:         "CDD Cajuru - Correios",
			CarrierCode:  "correios",
			CarrierName:  "Correios",
			FacilityType: "CDD",
			Neighborhood: "Cajuru",
			City:         "Curitiba",
			State:        "PR",
			Address:      "Av. Prefeito Maurício Fruet, 1650 - Cajuru",
			ZipCode:      "82900-010",
			Phone:        "(41) 3003-0100",
			OpeningHours: "Seg a Sex 09:00–17:00 | Sáb 09:00–12:00",
			Lat:          -25.4485,
			Lng:          -49.2165,
			Instructions: "CDD Cajuru Curitiba; retirada presencial no balcão interno com RG e código de rastreio.",
			Aliases:      []string{"cajuru", "cdd cajuru"},
		},
		{
			ID:           "cwb-cajuru-jadlog",
			Name:         "Unidade Jadlog Cajuru",
			CarrierCode:  "jadlog",
			CarrierName:  "Jadlog",
			FacilityType: "CDD",
			Neighborhood: "Cajuru",
			City:         "Curitiba",
			State:        "PR",
			Address:      "Rua Engenheiro Costa Barros, 920 - Cajuru",
			ZipCode:      "82940-010",
			Phone:        "(41) 3563-2000",
			OpeningHours: "Seg a Sex 08:30–18:00 | Sáb 08:30–12:00",
			Lat:          -25.4512,
			Lng:          -49.2194,
			Instructions: "Base operacional Jadlog Curitiba Leste / Cajuru.",
			Aliases:      []string{"jadlog cajuru", "jadlog curitiba"},
		},
		{
			ID:           "cwb-cajuru-total",
			Name:         "Hub Total Express Curitiba",
			CarrierCode:  "total_express",
			CarrierName:  "Total Express",
			FacilityType: "HUB",
			Neighborhood: "Cajuru",
			City:         "Curitiba",
			State:        "PR",
			Address:      "Rodovia BR-277, Km 79 - Cajuru",
			ZipCode:      "82930-000",
			Phone:        "(41) 3627-5900",
			OpeningHours: "Seg a Sex 08:00–18:00",
			Lat:          -25.4449,
			Lng:          -49.2118,
			Instructions: "Hub Paraná Total Express.",
			Aliases:      []string{"total curitiba", "total cajuru"},
		},
		{
			ID:           "cwb-centro-correios",
			Name:         "CDD / AC Central Curitiba - Correios",
			CarrierCode:  "correios",
			CarrierName:  "Correios",
			FacilityType: "CDD",
			Neighborhood: "Centro",
			City:         "Curitiba",
			State:        "PR",
			Address:      "Rua João Negrão, 1251 - Centro",
			ZipCode:      "80020-970",
			Phone:        "(41) 3003-0100",
			OpeningHours: "Seg a Sex 09:00–17:00 | Sáb 09:00–12:00",
			Lat:          -25.4296,
			Lng:          -49.2713,
			Instructions: "Unidade central Curitiba (CTCE/AC Central) para entrega e retirada no balcão.",
			Aliases:      []string{"curitiba", "cwb", "cte curitiba", "centro curitiba"},
		},
		{
			ID:           "cwb-centro-loggi",
			Name:         "Base Loggi Curitiba Centro",
			CarrierCode:  "loggi",
			CarrierName:  "Loggi",
			FacilityType: "CDD",
			Neighborhood: "Centro",
			City:         "Curitiba",
			State:        "PR",
			Address:      "Av. Sete de Setembro, 3150 - Centro",
			ZipCode:      "80230-010",
			Phone:        "(41) 4020-1460",
			OpeningHours: "Seg a Sáb 08:30–18:00",
			Lat:          -25.4328,
			Lng:          -49.2681,
			Instructions: "Base urbana Loggi Curitiba.",
			Aliases:      []string{"loggi curitiba"},
		},
		{
			ID:           "cwb-azul-cargo",
			Name:         "Azul Cargo Express Curitiba",
			CarrierCode:  "azul_cargo",
			CarrierName:  "Azul Cargo Express",
			FacilityType: "CDD",
			Neighborhood: "Centro",
			City:         "Curitiba",
			State:        "PR",
			Address:      "Av. Visconde de Guarapuava, 2810 - Centro",
			ZipCode:      "80010-100",
			Phone:        "(41) 4003-8399",
			OpeningHours: "Seg a Sex 08:00–19:00 | Sáb 08:00–13:00",
			Lat:          -25.4372,
			Lng:          -49.2764,
			Instructions: "Loja Azul Cargo Curitiba (conectada ao aeroporto Afonso Pena CWB).",
			Aliases:      []string{"azul curitiba", "afonso pena"},
		},

		// ==================== PORTO ALEGRE (RS) ====================
		{
			ID:           "poa-correios",
			Name:         "CTCE / CDD Porto Alegre Navegantes - Correios",
			CarrierCode:  "correios",
			CarrierName:  "Correios",
			FacilityType: "HUB",
			Neighborhood: "Navegantes",
			City:         "Porto Alegre",
			State:        "RS",
			Address:      "Av. Sertório, 4222 - Navegantes",
			ZipCode:      "91040-620",
			Phone:        "(51) 3003-0100",
			OpeningHours: "Seg a Sex 09:00–17:00 | Sáb 09:00–12:00",
			Lat:          -30.0029,
			Lng:          -51.1966,
			Instructions: "Centro de tratamento e retirada Porto Alegre Zona Norte com documento oficial.",
			Aliases:      []string{"porto alegre", "poa", "cte porto alegre", "navegantes"},
		},
		{
			ID:           "poa-azul",
			Name:         "Hub Azul Cargo Express Porto Alegre",
			CarrierCode:  "azul_cargo",
			CarrierName:  "Azul Cargo Express",
			FacilityType: "HUB",
			Neighborhood: "São João",
			City:         "Porto Alegre",
			State:        "RS",
			Address:      "Av. Severo Dullius, 800 - Aeroporto Salgado Filho",
			ZipCode:      "90200-310",
			Phone:        "(51) 4003-8399",
			OpeningHours: "Seg a Sex 07:30–20:00 | Sáb 08:00–14:00",
			Lat:          -29.9939,
			Lng:          -51.1711,
			Instructions: "Terminal de cargas Azul Cargo Aeroporto Salgado Filho (POA).",
			Aliases:      []string{"azul porto alegre", "salgado filho"},
		},
		{
			ID:           "poa-jadlog",
			Name:         "Unidade Jadlog Porto Alegre",
			CarrierCode:  "jadlog",
			CarrierName:  "Jadlog",
			FacilityType: "CDD",
			Neighborhood: "São Geraldo",
			City:         "Porto Alegre",
			State:        "RS",
			Address:      "Av. Pernambuco, 1890 - São Geraldo",
			ZipCode:      "90240-002",
			Phone:        "(51) 3563-2000",
			OpeningHours: "Seg a Sex 08:30–18:00 | Sáb 08:30–12:00",
			Lat:          -30.0085,
			Lng:          -51.1920,
			Instructions: "Unidade Jadlog Porto Alegre.",
			Aliases:      []string{"jadlog porto alegre"},
		},
		{
			ID:           "poa-loggi",
			Name:         "Base Loggi Porto Alegre Centro",
			CarrierCode:  "loggi",
			CarrierName:  "Loggi",
			FacilityType: "CDD",
			Neighborhood: "Centro Histórico",
			City:         "Porto Alegre",
			State:        "RS",
			Address:      "Rua Siqueira Campos, 1100 - Centro Histórico",
			ZipCode:      "90010-001",
			Phone:        "(51) 4020-1460",
			OpeningHours: "Seg a Sáb 08:30–18:00",
			Lat:          -30.0346,
			Lng:          -51.2177,
			Instructions: "Base urbana Loggi Porto Alegre.",
			Aliases:      []string{"loggi porto alegre"},
		},

		// ==================== BRASÍLIA (DF) ====================
		{
			ID:           "bsb-correios",
			Name:         "CTE / CDD Brasília SIA - Correios",
			CarrierCode:  "correios",
			CarrierName:  "Correios",
			FacilityType: "HUB",
			Neighborhood: "SIA",
			City:         "Brasília",
			State:        "DF",
			Address:      "SIA Trecho 3, Lote 1200 - Zona Industrial",
			ZipCode:      "71200-030",
			Phone:        "(61) 3003-0100",
			OpeningHours: "Seg a Sex 09:00–17:00 | Sáb 09:00–12:00",
			Lat:          -15.8025,
			Lng:          -47.9533,
			Instructions: "Unidade principal de tratamento e entrega em Brasília (SIA). Apresente documento com foto para retirada.",
			Aliases:      []string{"brasilia", "bsb", "sia", "cte brasilia", "distrito federal"},
		},
		{
			ID:           "bsb-total",
			Name:         "Hub Total Express Brasília",
			CarrierCode:  "total_express",
			CarrierName:  "Total Express",
			FacilityType: "HUB",
			Neighborhood: "SIA",
			City:         "Brasília",
			State:        "DF",
			Address:      "SIA Trecho 6, Lote 180 - Brasília",
			ZipCode:      "71205-060",
			Phone:        "(61) 3627-5900",
			OpeningHours: "Seg a Sex 08:00–18:00",
			Lat:          -15.8061,
			Lng:          -47.9578,
			Instructions: "Centro de distribuição Total Express Distrito Federal.",
			Aliases:      []string{"total brasilia"},
		},
		{
			ID:           "bsb-magalog",
			Name:         "Hub Magalog Brasília / Centro-Oeste",
			CarrierCode:  "magalog",
			CarrierName:  "Magalog",
			FacilityType: "HUB",
			Neighborhood: "SIA",
			City:         "Brasília",
			State:        "DF",
			Address:      "SIA Trecho 2, Lote 890 - Brasília",
			ZipCode:      "71200-020",
			Phone:        "(61) 3504-5500",
			OpeningHours: "Seg a Sex 08:00–18:00",
			Lat:          -15.7992,
			Lng:          -47.9491,
			Instructions: "Unidade regional Magalog Centro-Oeste.",
			Aliases:      []string{"magalog brasilia"},
		},
		{
			ID:           "bsb-azul",
			Name:         "Azul Cargo Express Aeroporto Brasília",
			CarrierCode:  "azul_cargo",
			CarrierName:  "Azul Cargo Express",
			FacilityType: "HUB",
			Neighborhood: "Lago Sul",
			City:         "Brasília",
			State:        "DF",
			Address:      "Aeroporto Internacional de Brasília, Terminal de Cargas",
			ZipCode:      "71608-900",
			Phone:        "(61) 4003-8399",
			OpeningHours: "Seg a Sex 07:00–21:00 | Sáb 08:00–15:00",
			Lat:          -15.8697,
			Lng:          -47.9172,
			Instructions: "Terminal Azul Cargo BSB para retirada expressa com AWB e documento oficial.",
			Aliases:      []string{"azul brasilia", "aeroporto brasilia"},
		},
		{
			ID:           "bsb-loggi",
			Name:         "Base Loggi Plano Piloto",
			CarrierCode:  "loggi",
			CarrierName:  "Loggi",
			FacilityType: "CDD",
			Neighborhood: "Asa Sul",
			City:         "Brasília",
			State:        "DF",
			Address:      "SCS Quadra 2, Bloco C - Asa Sul",
			ZipCode:      "70302-000",
			Phone:        "(61) 4020-1460",
			OpeningHours: "Seg a Sáb 08:30–18:00",
			Lat:          -15.7942,
			Lng:          -47.8822,
			Instructions: "Base Loggi Plano Piloto.",
			Aliases:      []string{"loggi brasilia", "asa sul", "plano piloto"},
		},

		// ==================== SALVADOR (BA) ====================
		{
			ID:           "ssa-correios",
			Name:         "CDD Salvador Pituba - Correios",
			CarrierCode:  "correios",
			CarrierName:  "Correios",
			FacilityType: "CDD",
			Neighborhood: "Pituba",
			City:         "Salvador",
			State:        "BA",
			Address:      "Av. Paulo VI, 190 - Pituba",
			ZipCode:      "41810-001",
			Phone:        "(71) 3003-0100",
			OpeningHours: "Seg a Sex 09:00–17:00 | Sáb 09:00–12:00",
			Lat:          -12.9998,
			Lng:          -38.4589,
			Instructions: "Retirada presencial no CDD Pituba mediante documento com foto e código de rastreio.",
			Aliases:      []string{"salvador", "ssa", "pituba", "cte salvador"},
		},
		{
			ID:           "ssa-azul",
			Name:         "Hub Azul Cargo Express Salvador",
			CarrierCode:  "azul_cargo",
			CarrierName:  "Azul Cargo Express",
			FacilityType: "HUB",
			Neighborhood: "São Cristóvão",
			City:         "Salvador",
			State:        "BA",
			Address:      "Praça Gago Coutinho, s/n - Terminal de Cargas Aeroporto SSA",
			ZipCode:      "41510-045",
			Phone:        "(71) 4003-8399",
			OpeningHours: "Seg a Sex 07:30–20:00 | Sáb 08:00–14:00",
			Lat:          -12.9089,
			Lng:          -38.3310,
			Instructions: "Terminal Azul Cargo Aeroporto de Salvador (SSA).",
			Aliases:      []string{"azul salvador"},
		},
		{
			ID:           "ssa-jadlog",
			Name:         "Unidade Jadlog Salvador",
			CarrierCode:  "jadlog",
			CarrierName:  "Jadlog",
			FacilityType: "CDD",
			Neighborhood: "Comércio",
			City:         "Salvador",
			State:        "BA",
			Address:      "Av. Estados Unidos, 340 - Comércio",
			ZipCode:      "40010-020",
			Phone:        "(71) 3563-2000",
			OpeningHours: "Seg a Sex 08:30–18:00 | Sáb 08:30–12:00",
			Lat:          -12.9714,
			Lng:          -38.5014,
			Instructions: "Filial Jadlog Salvador.",
			Aliases:      []string{"jadlog salvador"},
		},
		{
			ID:           "ssa-loggi",
			Name:         "Base Loggi Salvador",
			CarrierCode:  "loggi",
			CarrierName:  "Loggi",
			FacilityType: "CDD",
			Neighborhood: "Caminho das Árvores",
			City:         "Salvador",
			State:        "BA",
			Address:      "Av. Tancredo Neves, 1632 - Caminho das Árvores",
			ZipCode:      "41820-020",
			Phone:        "(71) 4020-1460",
			OpeningHours: "Seg a Sáb 08:30–18:00",
			Lat:          -12.9818,
			Lng:          -38.4651,
			Instructions: "Base metropolitana Loggi Salvador.",
			Aliases:      []string{"loggi salvador"},
		},

		// ==================== RECIFE (PE) ====================
		{
			ID:           "rec-correios",
			Name:         "CTCE / CDD Recife Imbiribeira - Correios",
			CarrierCode:  "correios",
			CarrierName:  "Correios",
			FacilityType: "HUB",
			Neighborhood: "Imbiribeira",
			City:         "Recife",
			State:        "PE",
			Address:      "Av. Mascarenhas de Moraes, 2244 - Imbiribeira",
			ZipCode:      "51150-002",
			Phone:        "(81) 3003-0100",
			OpeningHours: "Seg a Sex 09:00–17:00 | Sáb 09:00–12:00",
			Lat:          -8.1180,
			Lng:          -34.9165,
			Instructions: "Complexo Correios Imbiribeira Recife; apresente documento original e código para retirada.",
			Aliases:      []string{"recife", "rec", "imbiribeira", "cte recife"},
		},
		{
			ID:           "rec-azul",
			Name:         "Hub Azul Cargo Express Recife",
			CarrierCode:  "azul_cargo",
			CarrierName:  "Azul Cargo Express",
			FacilityType: "HUB",
			Neighborhood: "Boa Viagem",
			City:         "Recife",
			State:        "PE",
			Address:      "Praça Ministro Salgado Filho, s/n - TECA Recife",
			ZipCode:      "51210-902",
			Phone:        "(81) 4003-8399",
			OpeningHours: "Seg a Sex 07:00–21:00 | Sáb 08:00–15:00",
			Lat:          -8.1264,
			Lng:          -34.9229,
			Instructions: "Hub Nordeste Azul Cargo Express no Aeroporto dos Guararapes (REC).",
			Aliases:      []string{"azul recife", "guararapes"},
		},
		{
			ID:           "rec-total",
			Name:         "Hub Total Express Recife",
			CarrierCode:  "total_express",
			CarrierName:  "Total Express",
			FacilityType: "HUB",
			Neighborhood: "Imbiribeira",
			City:         "Recife",
			State:        "PE",
			Address:      "Av. Recife, 1890 - Imbiribeira",
			ZipCode:      "51170-000",
			Phone:        "(81) 3627-5900",
			OpeningHours: "Seg a Sex 08:00–18:00",
			Lat:          -8.1132,
			Lng:          -34.9128,
			Instructions: "Base regional Total Express Pernambuco.",
			Aliases:      []string{"total recife"},
		},
		{
			ID:           "rec-jadlog",
			Name:         "Unidade Jadlog Recife",
			CarrierCode:  "jadlog",
			CarrierName:  "Jadlog",
			FacilityType: "CDD",
			Neighborhood: "Santo Amaro",
			City:         "Recife",
			State:        "PE",
			Address:      "Av. Cruz Cabugá, 780 - Santo Amaro",
			ZipCode:      "50040-000",
			Phone:        "(81) 3563-2000",
			OpeningHours: "Seg a Sex 08:30–18:00 | Sáb 08:30–12:00",
			Lat:          -8.0578,
			Lng:          -34.8829,
			Instructions: "Unidade Jadlog Recife.",
			Aliases:      []string{"jadlog recife"},
		},

		// ==================== FORTALEZA (CE) ====================
		{
			ID:           "for-correios",
			Name:         "CDD Fortaleza Aldeota / Centro - Correios",
			CarrierCode:  "correios",
			CarrierName:  "Correios",
			FacilityType: "CDD",
			Neighborhood: "Aldeota",
			City:         "Fortaleza",
			State:        "CE",
			Address:      "Av. Santos Dumont, 2456 - Aldeota",
			ZipCode:      "60150-161",
			Phone:        "(85) 3003-0100",
			OpeningHours: "Seg a Sex 09:00–17:00 | Sáb 09:00–12:00",
			Lat:          -3.7319,
			Lng:          -38.5267,
			Instructions: "Unidade Correios Fortaleza para entrega e retirada presencial com documento oficial.",
			Aliases:      []string{"fortaleza", "aldeota", "cte fortaleza"},
		},
		{
			ID:           "for-azul",
			Name:         "Hub Azul Cargo Express Fortaleza",
			CarrierCode:  "azul_cargo",
			CarrierName:  "Azul Cargo Express",
			FacilityType: "HUB",
			Neighborhood: "Serrinha",
			City:         "Fortaleza",
			State:        "CE",
			Address:      "Av. Senador Carlos Jereissati, 3000 - TECA Fortaleza",
			ZipCode:      "60741-900",
			Phone:        "(85) 4003-8399",
			OpeningHours: "Seg a Sex 07:30–20:00 | Sáb 08:00–14:00",
			Lat:          -3.7763,
			Lng:          -38.5326,
			Instructions: "Terminal de cargas Azul Cargo Aeroporto Pinto Martins (FOR).",
			Aliases:      []string{"azul fortaleza", "pinto martins"},
		},
		{
			ID:           "for-jadlog",
			Name:         "Unidade Jadlog Fortaleza",
			CarrierCode:  "jadlog",
			CarrierName:  "Jadlog",
			FacilityType: "CDD",
			Neighborhood: "Joaquim Távora",
			City:         "Fortaleza",
			State:        "CE",
			Address:      "Av. Pontes Vieira, 1450 - Joaquim Távora",
			ZipCode:      "60135-238",
			Phone:        "(85) 3563-2000",
			OpeningHours: "Seg a Sex 08:30–18:00 | Sáb 08:30–12:00",
			Lat:          -3.7455,
			Lng:          -38.5189,
			Instructions: "Filial Jadlog Fortaleza.",
			Aliases:      []string{"jadlog fortaleza"},
		},
		{
			ID:           "for-magalog",
			Name:         "Hub Magalog Fortaleza / Ceará",
			CarrierCode:  "magalog",
			CarrierName:  "Magalog",
			FacilityType: "HUB",
			Neighborhood: "Passaré",
			City:         "Fortaleza",
			State:        "CE",
			Address:      "Av. Juscelino Kubitschek, 4100 - Passaré",
			ZipCode:      "60861-635",
			Phone:        "(85) 3504-5500",
			OpeningHours: "Seg a Sex 08:00–18:00",
			Lat:          -3.7812,
			Lng:          -38.5411,
			Instructions: "Centro de distribuição regional Magalog Ceará.",
			Aliases:      []string{"magalog fortaleza"},
		},

		// ==================== GOIÂNIA (GO) ====================
		{
			ID:           "gyn-correios",
			Name:         "CDD Goiânia Central - Correios",
			CarrierCode:  "correios",
			CarrierName:  "Correios",
			FacilityType: "CDD",
			Neighborhood: "Setor Central",
			City:         "Goiânia",
			State:        "GO",
			Address:      "Av. Anhanguera, 4210 - Setor Central",
			ZipCode:      "74043-010",
			Phone:        "(62) 3003-0100",
			OpeningHours: "Seg a Sex 09:00–17:00 | Sáb 09:00–12:00",
			Lat:          -16.6869,
			Lng:          -49.2648,
			Instructions: "Retirada presencial na unidade Goiânia com RG e código do objeto.",
			Aliases:      []string{"goiania", "gyn", "cte goiania"},
		},
		{
			ID:           "gyn-azul",
			Name:         "Azul Cargo Express Goiânia",
			CarrierCode:  "azul_cargo",
			CarrierName:  "Azul Cargo Express",
			FacilityType: "HUB",
			Neighborhood: "Santa Genoveva",
			City:         "Goiânia",
			State:        "GO",
			Address:      "Alameda 4, s/n - Terminal de Cargas Santa Genoveva",
			ZipCode:      "74672-856",
			Phone:        "(62) 4003-8399",
			OpeningHours: "Seg a Sex 08:00–19:00 | Sáb 08:00–13:00",
			Lat:          -16.6320,
			Lng:          -49.2207,
			Instructions: "Terminal Azul Cargo Aeroporto Santa Genoveva (GYN).",
			Aliases:      []string{"azul goiania", "santa genoveva"},
		},
		{
			ID:           "gyn-total",
			Name:         "Base Total Express Goiânia",
			CarrierCode:  "total_express",
			CarrierName:  "Total Express",
			FacilityType: "CDD",
			Neighborhood: "Setor Leste Vila Nova",
			City:         "Goiânia",
			State:        "GO",
			Address:      "Av. Independência, 2340 - Vila Nova",
			ZipCode:      "74645-010",
			Phone:        "(62) 3627-5900",
			OpeningHours: "Seg a Sex 08:00–18:00",
			Lat:          -16.6781,
			Lng:          -49.2542,
			Instructions: "Base Total Express Goiás.",
			Aliases:      []string{"total goiania"},
		},
		{
			ID:           "gyn-jadlog",
			Name:         "Unidade Jadlog Goiânia",
			CarrierCode:  "jadlog",
			CarrierName:  "Jadlog",
			FacilityType: "CDD",
			Neighborhood: "Setor Bueno",
			City:         "Goiânia",
			State:        "GO",
			Address:      "Av. T-63, 1890 - Setor Bueno",
			ZipCode:      "74230-100",
			Phone:        "(62) 3563-2000",
			OpeningHours: "Seg a Sex 08:30–18:00 | Sáb 08:30–12:00",
			Lat:          -16.6915,
			Lng:          -49.2724,
			Instructions: "Filial Jadlog Goiânia.",
			Aliases:      []string{"jadlog goiania"},
		},

		// ==================== MANAUS (AM) ====================
		{
			ID:           "mao-correios",
			Name:         "CDD Manaus Centro / Cachoeirinha - Correios",
			CarrierCode:  "correios",
			CarrierName:  "Correios",
			FacilityType: "CDD",
			Neighborhood: "Centro",
			City:         "Manaus",
			State:        "AM",
			Address:      "Rua Marcílio Dias, 165 - Centro",
			ZipCode:      "69005-270",
			Phone:        "(92) 3003-0100",
			OpeningHours: "Seg a Sex 08:30–16:30 | Sáb 09:00–12:00",
			Lat:          -3.1190,
			Lng:          -60.0217,
			Instructions: "Retirada presencial em Manaus mediante apresentação de documento oficial com foto.",
			Aliases:      []string{"manaus", "mao", "cte manaus"},
		},
		{
			ID:           "mao-azul",
			Name:         "Hub Azul Cargo Express Manaus",
			CarrierCode:  "azul_cargo",
			CarrierName:  "Azul Cargo Express",
			FacilityType: "HUB",
			Neighborhood: "Tarumã",
			City:         "Manaus",
			State:        "AM",
			Address:      "Av. Santos Dumont, 1350 - TECA Aeroporto Eduardo Gomes",
			ZipCode:      "69041-000",
			Phone:        "(92) 4003-8399",
			OpeningHours: "Seg a Sex 07:30–20:00 | Sáb 08:00–14:00",
			Lat:          -3.0386,
			Lng:          -60.0497,
			Instructions: "Terminal aéreo Azul Cargo Manaus (MAO) com atendimento para cargas expressas.",
			Aliases:      []string{"azul manaus", "eduardo gomes"},
		},
		{
			ID:           "mao-total",
			Name:         "Hub Total Express Manaus",
			CarrierCode:  "total_express",
			CarrierName:  "Total Express",
			FacilityType: "HUB",
			Neighborhood: "Distrito Industrial",
			City:         "Manaus",
			State:        "AM",
			Address:      "Av. Buriti, 2900 - Distrito Industrial I",
			ZipCode:      "69075-000",
			Phone:        "(92) 3627-5900",
			OpeningHours: "Seg a Sex 08:00–17:30",
			Lat:          -3.1055,
			Lng:          -59.9852,
			Instructions: "Base Total Express Polo Industrial de Manaus.",
			Aliases:      []string{"total manaus"},
		},
		{
			ID:           "mao-jadlog",
			Name:         "Unidade Jadlog Manaus",
			CarrierCode:  "jadlog",
			CarrierName:  "Jadlog",
			FacilityType: "CDD",
			Neighborhood: "Cachoeirinha",
			City:         "Manaus",
			State:        "AM",
			Address:      "Av. Castelo Branco, 1450 - Cachoeirinha",
			ZipCode:      "69065-011",
			Phone:        "(92) 3563-2000",
			OpeningHours: "Seg a Sex 08:30–17:30 | Sáb 08:30–12:00",
			Lat:          -3.1142,
			Lng:          -60.0128,
			Instructions: "Filial Jadlog Manaus.",
			Aliases:      []string{"jadlog manaus"},
		},
	}
}

func buildFallbackCities() []cityPoint {
	return []cityPoint{
		{City: "Cajamar", State: "SP", Lat: -23.3565, Lng: -46.8768, Aliases: []string{"cte cajamar", "hub cajamar"}},
		{City: "Guarulhos", State: "SP", Lat: -23.4356, Lng: -46.4731, Aliases: []string{"gru", "aeroporto guarulhos", "cumbica"}},
		{City: "Barueri", State: "SP", Lat: -23.5112, Lng: -46.8764, Aliases: []string{"alphaville", "tambore"}},
		{City: "Osasco", State: "SP", Lat: -23.5325, Lng: -46.7917},
		{City: "Santo André", State: "SP", Lat: -23.6639, Lng: -46.5383, Aliases: []string{"abc paulista"}},
		{City: "São Bernardo do Campo", State: "SP", Lat: -23.6914, Lng: -46.5646},
		{City: "Jundiaí", State: "SP", Lat: -23.1857, Lng: -46.8978},
		{City: "Ribeirão Preto", State: "SP", Lat: -21.1775, Lng: -47.8103},
		{City: "Sorocaba", State: "SP", Lat: -23.5015, Lng: -47.4526},
		{City: "Santos", State: "SP", Lat: -23.9608, Lng: -46.3336},
		{City: "São José dos Campos", State: "SP", Lat: -23.2237, Lng: -45.9009},
		{City: "Indaiatuba", State: "SP", Lat: -23.0903, Lng: -47.2181},
		{City: "Niterói", State: "RJ", Lat: -22.8833, Lng: -43.1036},
		{City: "Duque de Caxias", State: "RJ", Lat: -22.7858, Lng: -43.3117},
		{City: "Uberlândia", State: "MG", Lat: -18.9186, Lng: -48.2772},
		{City: "Juiz de Fora", State: "MG", Lat: -21.7642, Lng: -43.3503},
		{City: "Extrema", State: "MG", Lat: -22.8549, Lng: -46.3183},
		{City: "Londrina", State: "PR", Lat: -23.3045, Lng: -51.1696},
		{City: "Maringá", State: "PR", Lat: -23.4209, Lng: -51.9331},
		{City: "São José dos Pinhais", State: "PR", Lat: -25.5313, Lng: -49.2031},
		{City: "Florianópolis", State: "SC", Lat: -27.5954, Lng: -48.5480, Aliases: []string{"fln"}},
		{City: "Joinville", State: "SC", Lat: -26.3045, Lng: -48.8487},
		{City: "Itajaí", State: "SC", Lat: -26.9078, Lng: -48.6619},
		{City: "Blumenau", State: "SC", Lat: -26.9194, Lng: -49.0661},
		{City: "Canoas", State: "RS", Lat: -29.9178, Lng: -51.1836},
		{City: "Caxias do Sul", State: "RS", Lat: -29.1678, Lng: -51.1794},
		{City: "Vitória", State: "ES", Lat: -20.3155, Lng: -40.3128, Aliases: []string{"vix"}},
		{City: "Vila Velha", State: "ES", Lat: -20.3297, Lng: -40.2925},
		{City: "Serra", State: "ES", Lat: -20.1286, Lng: -40.3078},
		{City: "Natal", State: "RN", Lat: -5.7945, Lng: -35.2110},
		{City: "João Pessoa", State: "PB", Lat: -7.1195, Lng: -34.8450},
		{City: "Maceió", State: "AL", Lat: -9.6658, Lng: -35.7350},
		{City: "Aracaju", State: "SE", Lat: -10.9472, Lng: -37.0731},
		{City: "Teresina", State: "PI", Lat: -5.0919, Lng: -42.8034},
		{City: "São Luís", State: "MA", Lat: -2.5307, Lng: -44.3068},
		{City: "Belém", State: "PA", Lat: -1.4558, Lng: -48.4902},
		{City: "Campo Grande", State: "MS", Lat: -20.4697, Lng: -54.6201},
		{City: "Cuiabá", State: "MT", Lat: -15.6014, Lng: -56.0979},
		{City: "Palmas", State: "TO", Lat: -10.2491, Lng: -48.3243},
	}
}

func buildFallbackStates() map[string]cityPoint {
	return map[string]cityPoint{
		"SP": {City: "São Paulo", State: "SP", Lat: -23.5505, Lng: -46.6333},
		"RJ": {City: "Rio de Janeiro", State: "RJ", Lat: -22.9068, Lng: -43.1729},
		"MG": {City: "Belo Horizonte", State: "MG", Lat: -19.9167, Lng: -43.9345},
		"PR": {City: "Curitiba", State: "PR", Lat: -25.4284, Lng: -49.2733},
		"RS": {City: "Porto Alegre", State: "RS", Lat: -30.0346, Lng: -51.2177},
		"DF": {City: "Brasília", State: "DF", Lat: -15.7975, Lng: -47.8919},
		"BA": {City: "Salvador", State: "BA", Lat: -12.9714, Lng: -38.5014},
		"PE": {City: "Recife", State: "PE", Lat: -8.0476, Lng: -34.8770},
		"CE": {City: "Fortaleza", State: "CE", Lat: -3.7172, Lng: -38.5433},
		"GO": {City: "Goiânia", State: "GO", Lat: -16.6869, Lng: -49.2648},
		"AM": {City: "Manaus", State: "AM", Lat: -3.1190, Lng: -60.0217},
		"SC": {City: "Florianópolis", State: "SC", Lat: -27.5954, Lng: -48.5480},
		"ES": {City: "Vitória", State: "ES", Lat: -20.3155, Lng: -40.3128},
		"RN": {City: "Natal", State: "RN", Lat: -5.7945, Lng: -35.2110},
		"PB": {City: "João Pessoa", State: "PB", Lat: -7.1195, Lng: -34.8450},
		"AL": {City: "Maceió", State: "AL", Lat: -9.6658, Lng: -35.7350},
		"SE": {City: "Aracaju", State: "SE", Lat: -10.9472, Lng: -37.0731},
		"PI": {City: "Teresina", State: "PI", Lat: -5.0919, Lng: -42.8034},
		"MA": {City: "São Luís", State: "MA", Lat: -2.5307, Lng: -44.3068},
		"PA": {City: "Belém", State: "PA", Lat: -1.4558, Lng: -48.4902},
		"MS": {City: "Campo Grande", State: "MS", Lat: -20.4697, Lng: -54.6201},
		"MT": {City: "Cuiabá", State: "MT", Lat: -15.6014, Lng: -56.0979},
		"TO": {City: "Palmas", State: "TO", Lat: -10.2491, Lng: -48.3243},
		"RO": {City: "Porto Velho", State: "RO", Lat: -8.7612, Lng: -63.9004},
		"AC": {City: "Rio Branco", State: "AC", Lat: -9.9754, Lng: -67.8249},
		"AP": {City: "Macapá", State: "AP", Lat: 0.0356, Lng: -51.0705},
		"RR": {City: "Boa Vista", State: "RR", Lat: 2.8235, Lng: -60.6758},
	}
}
