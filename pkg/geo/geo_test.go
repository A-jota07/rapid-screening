package geo

import (
	"strings"
	"testing"
	"time"

	"rapid-screening/pkg/domain"
)

func TestCatalogEnricherImplementsDomainInterface(t *testing.T) {
	var _ domain.GeoEnricher = (*CatalogEnricher)(nil)
}

func TestCatalogCoverageCitiesAndCarriers(t *testing.T) {
	enricher := NewCatalogEnricher()
	entries := enricher.Entries()
	if len(entries) < 40 {
		t.Fatalf("expected rich Brazilian catalog, got %d entries", len(entries))
	}

	requiredCarriers := map[string]bool{
		"correios":      false,
		"loggi":         false,
		"azul_cargo":    false,
		"mandae":        false,
		"magalog":       false,
		"jadlog":        false,
		"total_express": false,
	}

	requiredLocations := map[string]bool{
		"vila mariana": false,
		"pinheiros":    false,
		"mooca":        false,
		"barra funda":  false,
		"viracopos":    false,
		"benfica":      false,
		"contagem":     false,
		"savassi":      false,
		"cajuru":       false,
		"porto alegre": false,
		"brasilia":     false,
		"salvador":     false,
		"recife":       false,
		"fortaleza":    false,
		"goiania":      false,
		"manaus":       false,
	}

	for _, e := range entries {
		if e.Lat == 0 || e.Lng == 0 {
			t.Errorf("entry %s has zero coordinates", e.ID)
		}
		if e.Address == "" || e.City == "" || e.State == "" || e.ZipCode == "" || e.Phone == "" || e.OpeningHours == "" || e.Instructions == "" {
			t.Errorf("entry %s has incomplete metadata: %+v", e.ID, e)
		}

		if _, ok := requiredCarriers[e.CarrierCode]; ok {
			requiredCarriers[e.CarrierCode] = true
		}

		blob := normalizeText(e.Name + " " + e.Neighborhood + " " + e.City)
		for loc := range requiredLocations {
			if strings.Contains(blob, loc) {
				requiredLocations[loc] = true
			}
		}
	}

	for carrierCode, found := range requiredCarriers {
		if !found {
			t.Errorf("missing catalog coverage for carrier %q", carrierCode)
		}
	}
	for loc, found := range requiredLocations {
		if !found {
			t.Errorf("missing catalog coverage for location %q", loc)
		}
	}
}

func TestEnrichReverseChronologicalEventsAndATCDD(t *testing.T) {
	enricher := NewCatalogEnricher()
	now := time.Now().UTC()

	result := &domain.TrackingResult{
		TrackingCode:       "BR123456789BR",
		Carrier:            domain.CarrierInfo{Code: "correios", Name: "Correios"},
		CurrentStatus:      domain.StatusAtCDD,
		CurrentStatusLabel: domain.StatusLabel(domain.StatusAtCDD),
		ProgressStep:       3,
		Events: []domain.TrackingEvent{
			{
				Timestamp:      now.Add(-1 * time.Hour),
				RawStatus:      "Aguardando retirada",
				StandardStatus: domain.StatusAtCDD,
				StandardLabel:  domain.StatusLabel(domain.StatusAtCDD),
				Description:    "Objeto aguardando retirada no endereço indicado",
				Location:       "CEE Vila Mariana - São Paulo/SP",
			},
			{
				Timestamp:      now.Add(-12 * time.Hour),
				RawStatus:      "Em trânsito",
				StandardStatus: domain.StatusInTransit,
				StandardLabel:  domain.StatusLabel(domain.StatusInTransit),
				Description:    "Objeto em transferência - por favor aguarde",
				Location:       "Campinas - SP",
			},
			{
				Timestamp:      now.Add(-36 * time.Hour),
				RawStatus:      "Postado",
				StandardStatus: domain.StatusPosted,
				StandardLabel:  domain.StatusLabel(domain.StatusPosted),
				Description:    "Objeto postado",
				Location:       "CTCE Benfica - Rio de Janeiro/RJ",
			},
		},
	}

	enricher.Enrich(result)

	for i, ev := range result.Events {
		if ev.Coordinates == nil || (ev.Coordinates.Lat == 0 && ev.Coordinates.Lng == 0) {
			t.Fatalf("event %d (%s) did not receive coordinates", i, ev.Location)
		}
	}

	if result.Origin == nil || result.Origin.State != "RJ" {
		t.Fatalf("expected Origin in RJ, got %+v", result.Origin)
	}
	if result.CurrentLocation == nil || result.CurrentLocation.City != "São Paulo" {
		t.Fatalf("expected CurrentLocation in São Paulo, got %+v", result.CurrentLocation)
	}
	if result.Destination == nil || result.Destination.City != "São Paulo" {
		t.Fatalf("expected Destination in São Paulo, got %+v", result.Destination)
	}

	if result.CDD == nil {
		t.Fatal("expected CDD to be populated for AT_CDD status")
	}
	if !result.CDD.AvailableForPickup {
		t.Error("expected CDD.AvailableForPickup=true for AT_CDD")
	}
	if !strings.Contains(normalizeText(result.CDD.Name), "vila mariana") {
		t.Errorf("expected Vila Mariana CDD, got %q", result.CDD.Name)
	}
	if result.CDD.Address == "" || result.CDD.OpeningHours == "" || result.CDD.Phone == "" || result.CDD.Instructions == "" {
		t.Errorf("expected full CDD pickup metadata, got %+v", result.CDD)
	}

	if len(result.RoutePoints) < 3 {
		t.Fatalf("expected at least 3 RoutePoints (RJ -> Campinas -> Vila Mariana), got %d", len(result.RoutePoints))
	}
	if result.RoutePoints[0].State != "RJ" {
		t.Errorf("expected first RoutePoint in RJ (chronological origin), got %+v", result.RoutePoints[0])
	}
	lastPt := result.RoutePoints[len(result.RoutePoints)-1]
	if lastPt.City != "São Paulo" {
		t.Errorf("expected last RoutePoint in São Paulo, got %+v", lastPt)
	}
}

func TestEnrichCarrierSpecificHubsAndAccentInsensitive(t *testing.T) {
	enricher := NewCatalogEnricher()
	now := time.Now().UTC()

	cases := []struct {
		name       string
		carrier    domain.CarrierInfo
		location   string
		status     domain.StandardStatus
		wantCity   string
		wantInName string
		wantPickup bool
	}{
		{
			name:       "Azul Cargo Viracopos Hub",
			carrier:    domain.CarrierInfo{Code: "azul_cargo", Name: "Azul Cargo Express"},
			location:   "Campinas / Viracopos - SP",
			status:     domain.StatusAtCDD,
			wantCity:   "Campinas",
			wantInName: "viracopos",
			wantPickup: true,
		},
		{
			name:       "Loggi Pinheiros",
			carrier:    domain.CarrierInfo{Code: "loggi", Name: "Loggi"},
			location:   "Pinheiros - Sao Paulo/SP",
			status:     domain.StatusOutForDelivery,
			wantCity:   "São Paulo",
			wantInName: "loggi pinheiros",
			wantPickup: false,
		},
		{
			name:       "Jadlog Mooca",
			carrier:    domain.CarrierInfo{Code: "jadlog", Name: "Jadlog"},
			location:   "Mooca, Sao Paulo - SP",
			status:     domain.StatusAtCDD,
			wantCity:   "São Paulo",
			wantInName: "jadlog mooca",
			wantPickup: true,
		},
		{
			name:       "Brasilia SIA Accentless",
			carrier:    domain.CarrierInfo{Code: "magalog", Name: "Magalog"},
			location:   "SIA - Brasilia/DF",
			status:     domain.StatusOutForDelivery,
			wantCity:   "Brasília",
			wantInName: "magalog brasilia",
			wantPickup: false,
		},
		{
			name:       "Manaus Eduardo Gomes",
			carrier:    domain.CarrierInfo{Code: "azul_cargo", Name: "Azul Cargo Express"},
			location:   "Manaus - AM",
			status:     domain.StatusAtCDD,
			wantCity:   "Manaus",
			wantInName: "azul cargo express manaus",
			wantPickup: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := &domain.TrackingResult{
				TrackingCode:  "TEST12345",
				Carrier:       tc.carrier,
				CurrentStatus: tc.status,
				Events: []domain.TrackingEvent{
					{
						Timestamp:      now,
						StandardStatus: tc.status,
						Description:    "Atualização operacional",
						Location:       tc.location,
					},
				},
			}
			enricher.Enrich(res)

			if res.CDD == nil {
				t.Fatalf("expected CDD to be resolved for %s", tc.location)
			}
			if res.CDD.City != tc.wantCity {
				t.Errorf("got CDD city %q, want %q", res.CDD.City, tc.wantCity)
			}
			if !strings.Contains(normalizeText(res.CDD.Name), tc.wantInName) {
				t.Errorf("got CDD name %q, want substring %q", res.CDD.Name, tc.wantInName)
			}
			if res.CDD.AvailableForPickup != tc.wantPickup {
				t.Errorf("got AvailableForPickup=%v, want %v", res.CDD.AvailableForPickup, tc.wantPickup)
			}
			if len(res.RoutePoints) == 0 {
				t.Error("expected non-empty RoutePoints")
			}
		})
	}
}

func TestEnrichKnownDestinationDuringTransitAndPreserveExistingCoords(t *testing.T) {
	enricher := NewCatalogEnricher()
	now := time.Now().UTC()

	res := &domain.TrackingResult{
		TrackingCode:  "JAD998877",
		Carrier:       domain.CarrierInfo{Code: "jadlog", Name: "Jadlog"},
		CurrentStatus: domain.StatusInTransit,
		Destination: &domain.GeoPoint{
			City:  "Curitiba",
			State: "PR",
		},
		Events: []domain.TrackingEvent{
			{
				Timestamp:      now,
				StandardStatus: domain.StatusInTransit,
				Description:    "Transferência para unidade destino",
				Location:       "Coleta Customizada",
				Coordinates: &domain.GeoPoint{
					Lat:          -23.4001,
					Lng:          -46.7002,
					City:         "São Paulo",
					State:        "SP",
					FacilityType: "HUB",
					FacilityName: "Hub Custom",
				},
			},
		},
	}

	enricher.Enrich(res)

	// Deve preservar coordenadas existentes.
	if res.Events[0].Coordinates.Lat != -23.4001 || res.Events[0].Coordinates.Lng != -46.7002 {
		t.Errorf("existing coordinates were overwritten: %+v", res.Events[0].Coordinates)
	}
	// Destination sem lat/lng deve ser preenchido.
	if res.Destination.Lat == 0 || res.Destination.Lng == 0 {
		t.Errorf("expected Destination coordinates to be enriched, got %+v", res.Destination)
	}
	// CDD de destino deve ser informado (AvailableForPickup = false durante trânsito).
	if res.CDD == nil || res.CDD.City != "Curitiba" {
		t.Fatalf("expected Curitiba CDD from known destination, got %+v", res.CDD)
	}
	if res.CDD.AvailableForPickup {
		t.Error("expected AvailableForPickup=false while IN_TRANSIT")
	}
	if len(res.RoutePoints) < 2 {
		t.Errorf("expected at least 2 RoutePoints (Hub -> Destination), got %d", len(res.RoutePoints))
	}
}

func TestEnrichNilSafeAndSingleEventRealAPI(t *testing.T) {
	enricher := NewCatalogEnricher()
	enricher.Enrich(nil) // não deve dar panic

	res := &domain.TrackingResult{
		TrackingCode:  "REAL17TRACK",
		Carrier:       domain.CarrierInfo{Code: "correios", Name: "Correios"},
		CurrentStatus: domain.StatusDelivered,
		Events: []domain.TrackingEvent{
			{
				Timestamp:      time.Now().UTC(),
				RawStatus:      "Delivered",
				StandardStatus: domain.StatusDelivered,
				Description:    "Package delivered in Fortaleza, CE, Brazil",
				Location:       "Fortaleza, CE, Brazil",
			},
		},
	}

	enricher.Enrich(res)
	if len(res.RoutePoints) == 0 {
		t.Fatal("expected RoutePoints for real API result")
	}
	if res.CurrentLocation == nil || res.CurrentLocation.City != "Fortaleza" {
		t.Fatalf("expected CurrentLocation in Fortaleza, got %+v", res.CurrentLocation)
	}
	if res.Destination == nil || res.Destination.City != "Fortaleza" {
		t.Fatalf("expected Destination in Fortaleza, got %+v", res.Destination)
	}
}
