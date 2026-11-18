package providers

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	carrierpkg "rapid-screening/pkg/carrier"
	"rapid-screening/pkg/domain"
)

type brazilCityHub struct {
	City          string
	State         string
	Lat           float64
	Lng           float64
	OriginAddress string
	DestAddress   string
	OriginCEP     string
	DestCEP       string
	HubName       string
	CDDName       string
	CDDAddress    string
	CDDCEP        string
	CDDPhone      string
}

var brazilHubs = []brazilCityHub{
	{
		City: "São Paulo", State: "SP", Lat: -23.5505, Lng: -46.6333,
		OriginAddress: "Av. Jaguaré, 1485 - Jaguaré", DestAddress: "Rua Domingos de Morais, 2180 - Vila Mariana",
		OriginCEP: "05346-900", DestCEP: "04036-000",
		HubName: "CTE Jaguaré / São Paulo", CDDName: "Vila Mariana",
		CDDAddress: "Rua Pelotas, 320 - Vila Mariana", CDDCEP: "04012-001", CDDPhone: "(11) 3003-0100",
	},
	{
		City: "Cajamar", State: "SP", Lat: -23.3550, Lng: -46.8783,
		OriginAddress: "Rod. Anhanguera, Km 38 - Empresarial Anhanguera", DestAddress: "Av. Tenente Marques, 1850 - Polvilho",
		OriginCEP: "07750-000", DestCEP: "07790-240",
		HubName: "Centro Logístico Cajamar", CDDName: "Cajamar Centro",
		CDDAddress: "Av. Deovair Cruz de Oliveira, 410 - Jordanésia", CDDCEP: "07760-000", CDDPhone: "(11) 4446-8200",
	},
	{
		City: "Campinas", State: "SP", Lat: -22.9099, Lng: -47.0626,
		OriginAddress: "Rod. Santos Dumont, Km 66 - Viracopos", DestAddress: "Av. Barão de Itapura, 1480 - Guanabara",
		OriginCEP: "13052-900", DestCEP: "13020-432",
		HubName: "Hub Aeroportuário Viracopos", CDDName: "Campinas Cambuí",
		CDDAddress: "Rua Coronel Quirino, 910 - Cambuí", CDDCEP: "13025-001", CDDPhone: "(19) 3251-4400",
	},
	{
		City: "Rio de Janeiro", State: "RJ", Lat: -22.9068, Lng: -43.1729,
		OriginAddress: "Rua da Praia de São Cristóvão, 290 - Benfica", DestAddress: "Rua Voluntários da Pátria, 340 - Botafogo",
		OriginCEP: "20921-440", DestCEP: "22270-016",
		HubName: "CTE Benfica / Rio de Janeiro", CDDName: "Botafogo",
		CDDAddress: "Rua São Clemente, 185 - Botafogo", CDDCEP: "22260-001", CDDPhone: "(21) 3003-0100",
	},
	{
		City: "Belo Horizonte", State: "MG", Lat: -19.9167, Lng: -43.9345,
		OriginAddress: "Via Expressa de Contagem, 3100 - Camargos", DestAddress: "Av. Cristóvão Colombo, 520 - Savassi",
		OriginCEP: "30580-000", DestCEP: "30140-140",
		HubName: "Hub Metropolitano Contagem", CDDName: "Savassi",
		CDDAddress: "Rua Pernambuco, 1020 - Funcionários", CDDCEP: "30130-151", CDDPhone: "(31) 3261-7100",
	},
	{
		City: "Curitiba", State: "PR", Lat: -25.4284, Lng: -49.2733,
		OriginAddress: "Rua João Negrão, 1251 - Rebouças", DestAddress: "Av. Sete de Setembro, 4476 - Batel",
		OriginCEP: "80230-150", DestCEP: "80250-210",
		HubName: "CTE Curitiba / Triagem Sul", CDDName: "Batel",
		CDDAddress: "Rua Buenos Aires, 640 - Batel", CDDCEP: "80250-070", CDDPhone: "(41) 3310-2200",
	},
	{
		City: "Porto Alegre", State: "RS", Lat: -30.0346, Lng: -51.2177,
		OriginAddress: "Av. Sertório, 4222 - Sarandi", DestAddress: "Rua Padre Chagas, 240 - Moinhos de Vento",
		OriginCEP: "91040-620", DestCEP: "90570-080",
		HubName: "CTCE Porto Alegre", CDDName: "Moinhos de Vento",
		CDDAddress: "Av. 24 de Outubro, 850 - Moinhos de Vento", CDDCEP: "90510-000", CDDPhone: "(51) 3220-5500",
	},
	{
		City: "Salvador", State: "BA", Lat: -12.9714, Lng: -38.5014,
		OriginAddress: "Rod. BR-324, Km 18 - Simões Filho", DestAddress: "Av. Paulo VI, 1340 - Pituba",
		OriginCEP: "43700-000", DestCEP: "41810-001",
		HubName: "Hub Metropolitano Salvador", CDDName: "Pituba",
		CDDAddress: "Rua Ceará, 412 - Pituba", CDDCEP: "41830-451", CDDPhone: "(71) 3340-6100",
	},
	{
		City: "Recife", State: "PE", Lat: -8.0476, Lng: -34.8770,
		OriginAddress: "Rod. BR-101 Sul, Km 82 - Jaboatão", DestAddress: "Av. Conselheiro Aguiar, 2330 - Boa Viagem",
		OriginCEP: "54345-000", DestCEP: "51020-020",
		HubName: "Hub Nordeste Recife", CDDName: "Boa Viagem",
		CDDAddress: "Rua dos Navegantes, 1180 - Boa Viagem", CDDCEP: "51020-010", CDDPhone: "(81) 3465-9000",
	},
	{
		City: "Brasília", State: "DF", Lat: -15.7975, Lng: -47.8919,
		OriginAddress: "SIA Trecho 3 Lote 1200 - Guará", DestAddress: "CLN 208 Bloco B Loja 14 - Asa Norte",
		OriginCEP: "71200-030", DestCEP: "70853-520",
		HubName: "CTCE Brasília / Planalto", CDDName: "Asa Norte",
		CDDAddress: "SCRN 704/705 Bloco D - Asa Norte", CDDCEP: "70730-640", CDDPhone: "(61) 3426-8800",
	},
	{
		City: "Goiânia", State: "GO", Lat: -16.6869, Lng: -49.2648,
		OriginAddress: "Av. São Paulo, 980 - Aparecida de Goiânia", DestAddress: "Av. T-10, 1040 - Setor Bueno",
		OriginCEP: "74910-000", DestCEP: "74223-060",
		HubName: "Hub Centro-Oeste Goiânia", CDDName: "Setor Bueno",
		CDDAddress: "Rua T-36, 2150 - Setor Bueno", CDDCEP: "74223-052", CDDPhone: "(62) 3251-4100",
	},
	{
		City: "Fortaleza", State: "CE", Lat: -3.7319, Lng: -38.5267,
		OriginAddress: "Av. Parque Oeste, 1500 - Maracanaú", DestAddress: "Av. Santos Dumont, 2800 - Aldeota",
		OriginCEP: "61939-000", DestCEP: "60150-161",
		HubName: "Hub Logístico Fortaleza", CDDName: "Aldeota",
		CDDAddress: "Rua Barbosa de Freitas, 990 - Aldeota", CDDCEP: "60170-021", CDDPhone: "(85) 3261-3300",
	},
	{
		City: "Florianópolis", State: "SC", Lat: -27.5954, Lng: -48.5480,
		OriginAddress: "Rod. BR-101, Km 205 - São José", DestAddress: "Rua Bocaiúva, 2125 - Centro",
		OriginCEP: "88110-000", DestCEP: "88015-902",
		HubName: "Hub Litoral Sul / Grande Floripa", CDDName: "Centro Florianópolis",
		CDDAddress: "Av. Mauro Ramos, 1450 - Centro", CDDCEP: "88020-302", CDDPhone: "(48) 3224-1800",
	},
}

// SmartFallbackProvider gera de forma determinística (via SHA-256 do código) uma linha do tempo
// realista com coordenadas brasileiras válidas e alocação de CDD, garantindo funcionamento 100% offline.
type SmartFallbackProvider struct {
	state *providerState
	nowFn func() time.Time
}

// NewSmartFallbackProvider instancia o provedor determinístico de fallback local.
func NewSmartFallbackProvider() *SmartFallbackProvider {
	return &SmartFallbackProvider{
		state: newProviderState("smart_demo_fallback", "local_fallback", true, true, -1),
		nowFn: time.Now,
	}
}

// Name retorna "smart_demo_fallback".
func (p *SmartFallbackProvider) Name() string {
	return "smart_demo_fallback"
}

// Status retorna a disponibilidade permanente e gratuita do fallback local.
func (p *SmartFallbackProvider) Status() domain.ProviderHealth {
	return p.state.health()
}

// Track produz um resultado de rastreamento completo e determinístico baseado no hash do código.
func (p *SmartFallbackProvider) Track(ctx context.Context, code string, carrierInfo domain.CarrierInfo) (*domain.TrackingResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	start := time.Now()
	cleanCode := carrierpkg.SanitizeCode(code)
	if cleanCode == "" {
		err := fmt.Errorf("%w: empty tracking code", ErrNotFound)
		p.state.recordFailure(time.Since(start), err, 0)
		return nil, err
	}

	if err := p.state.checkReady(); err != nil {
		return nil, err
	}

	if carrierInfo.Code == "" {
		carrierInfo = carrierpkg.DetectCarrier(cleanCode)
	}

	hash := sha256.Sum256([]byte(cleanCode))
	targetStatus := resolveTargetStatus(cleanCode, hash)

	originHub, transitHub, destHub := selectDeterministicRoute(hash)

	originPoint := domain.GeoPoint{
		Lat:          originHub.Lat,
		Lng:          originHub.Lng,
		City:         originHub.City,
		State:        originHub.State,
		Address:      originHub.OriginAddress,
		ZipCode:      originHub.OriginCEP,
		FacilityType: "ORIGIN",
		FacilityName: fmt.Sprintf("Unidade de Postagem %s - %s", shortCarrierLabel(carrierInfo), originHub.City),
	}

	hubPoint := domain.GeoPoint{
		Lat:          transitHub.Lat,
		Lng:          transitHub.Lng,
		City:         transitHub.City,
		State:        transitHub.State,
		Address:      transitHub.OriginAddress,
		ZipCode:      transitHub.OriginCEP,
		FacilityType: "HUB",
		FacilityName: transitHub.HubName,
	}

	cddPoint := domain.GeoPoint{
		Lat:          roundCoord(destHub.Lat - 0.0115),
		Lng:          roundCoord(destHub.Lng + 0.0125),
		City:         destHub.City,
		State:        destHub.State,
		Address:      destHub.CDDAddress,
		ZipCode:      destHub.CDDCEP,
		FacilityType: "CDD",
		FacilityName: fmt.Sprintf("CDD %s - %s", shortCarrierLabel(carrierInfo), destHub.CDDName),
	}

	destPoint := domain.GeoPoint{
		Lat:          roundCoord(destHub.Lat + 0.0082),
		Lng:          roundCoord(destHub.Lng - 0.0094),
		City:         destHub.City,
		State:        destHub.State,
		Address:      destHub.DestAddress,
		ZipCode:      destHub.DestCEP,
		FacilityType: "DESTINATION",
		FacilityName: fmt.Sprintf("Endereço do Destinatário - %s/%s", destHub.City, destHub.State),
	}

	cddAlloc := buildCDDAllocation(destHub, cddPoint, carrierInfo, targetStatus)

	now := p.nowFn().UTC()
	baseDay := now.Truncate(24 * time.Hour)
	events := buildDeterministicEvents(cleanCode, carrierInfo, targetStatus, hash, baseDay, originPoint, hubPoint, cddPoint, destPoint)

	currentLoc := currentLocationForStatus(targetStatus, originPoint, hubPoint, cddPoint, destPoint)
	estDelivery := estimatedDeliveryForStatus(targetStatus, baseDay, hash)

	_, _, progressStep := carrierpkg.NormalizeStatus(events[0].RawStatus)
	result := &domain.TrackingResult{
		TrackingCode:       cleanCode,
		Carrier:            carrierInfo,
		CurrentStatus:      targetStatus,
		CurrentStatusLabel: domain.StatusLabel(targetStatus),
		ProgressStep:       progressStep,
		EstimatedDelivery:  estDelivery,
		Origin:             &originPoint,
		Destination:        &destPoint,
		CurrentLocation:    &currentLoc,
		CDD:                cddAlloc,
		RoutePoints:        []domain.GeoPoint{originPoint, hubPoint, cddPoint, destPoint},
		Events:             events,
		ProviderUsed:       p.Name(),
		Cached:             false,
		CacheExpiresAt:     now.Add(5 * time.Minute),
		LastUpdatedAt:      now,
	}

	finalizeTrackingResult(result, cleanCode, carrierInfo, p.Name(), now)
	p.state.recordSuccess(time.Since(start))
	return result, nil
}

func resolveTargetStatus(cleanCode string, hash [32]byte) domain.StandardStatus {
	switch {
	case strings.HasSuffix(cleanCode, "CDD") || cleanCode == "AA123456789BR" || cleanCode == "MND994827162BR":
		return domain.StatusAtCDD
	case strings.HasSuffix(cleanCode, "ROTA") || cleanCode == "LGG84920194BR":
		return domain.StatusOutForDelivery
	case strings.HasSuffix(cleanCode, "ENT") || cleanCode == "MGL339104857BR":
		return domain.StatusDelivered
	case cleanCode == "AZL577901234BR":
		return domain.StatusInTransit
	default:
		statuses := []domain.StandardStatus{
			domain.StatusPosted,
			domain.StatusInTransit,
			domain.StatusAtCDD,
			domain.StatusOutForDelivery,
			domain.StatusDelivered,
			domain.StatusException,
		}
		return statuses[int(hash[0])%len(statuses)]
	}
}

func selectDeterministicRoute(hash [32]byte) (brazilCityHub, brazilCityHub, brazilCityHub) {
	n := len(brazilHubs)
	originIdx := int(hash[1]) % n
	destIdx := (originIdx + 1 + int(hash[2])%(n-1)) % n
	hubIdx := (originIdx + 1 + int(hash[3])%(n-1)) % n
	if hubIdx == destIdx {
		hubIdx = (hubIdx + 1) % n
		if hubIdx == originIdx {
			hubIdx = (hubIdx + 1) % n
		}
	}
	return brazilHubs[originIdx], brazilHubs[hubIdx], brazilHubs[destIdx]
}

func shortCarrierLabel(info domain.CarrierInfo) string {
	switch info.Code {
	case "correios":
		return "Correios"
	case "azul_express":
		return "Azul Cargo"
	case "loggi":
		return "Loggi"
	case "mandae":
		return "Mandaê"
	case "magalog":
		return "Magalog"
	case "jadlog":
		return "Jadlog"
	case "total_express":
		return "Total Express"
	case "sequoia":
		return "Sequoia"
	case "cainiao":
		return "Cainiao"
	case "jt_express":
		return "J&T Express"
	default:
		return "Hub Brasil"
	}
}

func buildCDDAllocation(destHub brazilCityHub, cddPoint domain.GeoPoint, info domain.CarrierInfo, status domain.StandardStatus) *domain.CDDAllocation {
	carrierName := info.Name
	if carrierName == "" {
		carrierName = "Transportadora Parceira"
	}

	available := status == domain.StatusAtCDD
	instructions := "Encomenda ainda em trânsito para a unidade de distribuição local."
	switch status {
	case domain.StatusAtCDD:
		instructions = "Disponível para retirada presencial no balcão do CDD. Apresente documento oficial com foto e o código de rastreio."
	case domain.StatusOutForDelivery:
		instructions = "Pacote já saiu do CDD com o motorista para entrega no endereço de destino."
	case domain.StatusDelivered:
		instructions = "Entrega finalizada. Unidade CDD responsável apenas como referência histórica da rota."
	case domain.StatusException:
		instructions = "Ocorrência registrada na entrega; entre em contato com o CDD para confirmar reentrega ou retirada."
	case domain.StatusPosted:
		instructions = "Objeto recém-postado na origem; previsão de alocação neste CDD na etapa final da rota."
	}

	return &domain.CDDAllocation{
		Name:               cddPoint.FacilityName,
		CarrierName:        carrierName,
		Address:            destHub.CDDAddress,
		City:               destHub.City,
		State:              destHub.State,
		ZipCode:            destHub.CDDCEP,
		Phone:              destHub.CDDPhone,
		OpeningHours:       "Seg a Sex: 08h00 - 18h00 | Sáb: 08h00 - 12h00",
		Lat:                cddPoint.Lat,
		Lng:                cddPoint.Lng,
		AvailableForPickup: available,
		Instructions:       instructions,
	}
}

func buildDeterministicEvents(
	cleanCode string,
	carrierInfo domain.CarrierInfo,
	target domain.StandardStatus,
	hash [32]byte,
	baseDay time.Time,
	origin, hub, cdd, dest domain.GeoPoint,
) []domain.TrackingEvent {
	var events []domain.TrackingEvent

	addEvent := func(dayOffset, hourOffset, minOffset int, rawMsg, detail string, pt domain.GeoPoint) {
		ts := baseDay.AddDate(0, 0, dayOffset).Add(time.Duration(hourOffset)*time.Hour + time.Duration(minOffset)*time.Minute)
		st, lbl, _ := carrierpkg.NormalizeStatus(rawMsg)
		loc := fmt.Sprintf("%s - %s", pt.City, pt.State)
		ptCopy := pt
		events = append(events, domain.TrackingEvent{
			Timestamp:      ts,
			RawStatus:      rawMsg,
			StandardStatus: st,
			StandardLabel:  lbl,
			Description:    detail,
			Location:       loc,
			Coordinates:    &ptCopy,
		})
	}

	h1 := 9 + int(hash[4]%4)
	m1 := int(hash[5] % 55)
	h2 := 13 + int(hash[6]%5)
	m2 := int(hash[7] % 55)
	h3 := 8 + int(hash[8]%4)
	m3 := int(hash[9] % 55)

	postedRaw, postedDesc := postedCopyForCarrier(carrierInfo, cleanCode)
	addEvent(-4, h1, m1, postedRaw, postedDesc, origin)
	if target == domain.StatusPosted {
		sortEventsDesc(events)
		return events
	}

	addEvent(-3, h2, m2, "Processado no centro logístico", fmt.Sprintf("Triagem automatizada concluída em %s", hub.FacilityName), hub)
	transitRaw, transitDesc := transitCopyForCarrier(carrierInfo, origin, dest)
	addEvent(-2, h1, m3, transitRaw, transitDesc, hub)
	if target == domain.StatusInTransit {
		sortEventsDesc(events)
		return events
	}

	if target == domain.StatusException {
		excMessages := []string{
			"Destinatário ausente",
			"Tentativa de entrega não efetuada",
			"Endereço incorreto",
		}
		excRaw := excMessages[int(hash[10])%len(excMessages)]
		addEvent(-1, h2, m1, excRaw, fmt.Sprintf("%s na área atendida por %s. Pacote retornou para triagem na unidade.", excRaw, cdd.FacilityName), cdd)
		sortEventsDesc(events)
		return events
	}

	cddRaw := "Chegou na unidade de distribuição - Alocado no CDD"
	cddDesc := fmt.Sprintf("Objeto alocado em %s (%s) e disponível para retirada ou expedição local.", cdd.FacilityName, cdd.Address)
	if target == domain.StatusAtCDD {
		cddRaw = "Disponível para retirada - Alocado no CDD"
	}
	addEvent(-1, h3, m2, cddRaw, cddDesc, cdd)
	if target == domain.StatusAtCDD {
		sortEventsDesc(events)
		return events
	}

	rotaRaw, rotaDesc := outForDeliveryCopyForCarrier(carrierInfo, cdd)
	addEvent(0, h3, m1, rotaRaw, rotaDesc, cdd)
	if target == domain.StatusOutForDelivery {
		sortEventsDesc(events)
		return events
	}

	delivRaw, delivDesc := deliveredCopyForCarrier(carrierInfo, dest)
	addEvent(0, h2, m3, delivRaw, delivDesc, dest)
	sortEventsDesc(events)
	return events
}

func postedCopyForCarrier(info domain.CarrierInfo, code string) (string, string) {
	switch info.Code {
	case "azul_express":
		return "Etiqueta emitida", fmt.Sprintf("Conhecimento aéreo (AWB) %s registrado na base Azul Cargo", code)
	case "loggi":
		return "Pedido recebido pela transportadora", fmt.Sprintf("Coleta solicitada e etiqueta %s gerada na rede Loggi", code)
	default:
		return "Objeto postado", fmt.Sprintf("Volume %s postado e recebido na unidade de origem", code)
	}
}

func transitCopyForCarrier(info domain.CarrierInfo, origin, dest domain.GeoPoint) (string, string) {
	switch info.Code {
	case "azul_express":
		return "Em transferência", fmt.Sprintf("Carga em transferência aérea de %s/%s para %s/%s", origin.City, origin.State, dest.City, dest.State)
	case "correios":
		return "Objeto em trânsito", fmt.Sprintf("Objeto em trânsito de %s/%s para unidade de distribuição em %s/%s", origin.City, origin.State, dest.City, dest.State)
	default:
		return "Em transferência", fmt.Sprintf("Remessa encaminhada entre hubs regionais rumo a %s/%s", dest.City, dest.State)
	}
}

func outForDeliveryCopyForCarrier(info domain.CarrierInfo, cdd domain.GeoPoint) (string, string) {
	switch info.Code {
	case "loggi":
		return "Motorista a caminho - Em rota de entrega", fmt.Sprintf("Entregador Loggi em rota de entrega a partir de %s", cdd.FacilityName)
	case "correios":
		return "Objeto saiu para entrega ao destinatário", fmt.Sprintf("Carteiro saiu para entrega ao destinatário (%s)", cdd.FacilityName)
	default:
		return "Saiu para entrega", fmt.Sprintf("Veículo em deslocamento para entrega final a partir de %s", cdd.FacilityName)
	}
}

func deliveredCopyForCarrier(info domain.CarrierInfo, dest domain.GeoPoint) (string, string) {
	switch info.Code {
	case "correios":
		return "Objeto entregue ao destinatário", fmt.Sprintf("Objeto entregue ao destinatário em %s/%s", dest.City, dest.State)
	case "loggi":
		return "Pacote entregue", fmt.Sprintf("Entrega confirmada com assinatura digital em %s/%s", dest.City, dest.State)
	default:
		return "Entrega realizada", fmt.Sprintf("Entrega realizada com sucesso no endereço de destino em %s/%s", dest.City, dest.State)
	}
}

func currentLocationForStatus(status domain.StandardStatus, origin, hub, cdd, dest domain.GeoPoint) domain.GeoPoint {
	switch status {
	case domain.StatusPosted:
		return origin
	case domain.StatusInTransit, domain.StatusException:
		return hub
	case domain.StatusAtCDD, domain.StatusOutForDelivery:
		return cdd
	case domain.StatusDelivered:
		return dest
	default:
		return hub
	}
}

func estimatedDeliveryForStatus(status domain.StandardStatus, baseDay time.Time, hash [32]byte) string {
	switch status {
	case domain.StatusDelivered:
		return fmt.Sprintf("Entregue em %s", baseDay.Format("02/01/2006"))
	case domain.StatusOutForDelivery, domain.StatusAtCDD:
		return baseDay.Format("2006-01-02")
	default:
		days := 1 + int(hash[11]%3)
		return baseDay.AddDate(0, 0, days).Format("2006-01-02")
	}
}

func roundCoord(v float64) float64 {
	return float64(int(v*10000)) / 10000
}
