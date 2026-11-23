package carrier

import (
	"testing"

	"rapid-screening/pkg/domain"
)

func TestSanitizeCode(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"aa123456789br", "AA123456789BR"},
		{" aa 123-456 789-br \n\t", "AA123456789BR"},
		{"577-12345678", "57712345678"},
		{"  lgg-84920194 br ", "LGG84920194BR"},
		{"$$$$$!!??../", ""},
		{"AA123456789BR?foo=bar", "AA123456789BRFOOBAR"},
		{"", ""},
	}

	for _, tc := range cases {
		got := SanitizeCode(tc.input)
		if got != tc.want {
			t.Errorf("SanitizeCode(%q) = %q; want %q", tc.input, got, tc.want)
		}
	}
}

func TestDetectCarrier(t *testing.T) {
	cases := []struct {
		name           string
		code           string
		wantCode       string
		wantName       string
		want17ID       int
		wantTMCode     string
		wantDetectedBy string
		wantMinConf    float64
		wantExactConf  float64
	}{
		{
			name:           "Correios demo",
			code:           "AA123456789BR",
			wantCode:       "correios",
			wantName:       "Correios",
			want17ID:       2151,
			wantTMCode:     "brazil-correios",
			wantDetectedBy: "regex",
			wantMinConf:    0.90,
		},
		{
			name:           "Correios lowercase com espaco e hifen",
			code:           "nl 987-654-321 br",
			wantCode:       "correios",
			wantName:       "Correios",
			want17ID:       2151,
			wantTMCode:     "brazil-correios",
			wantDetectedBy: "regex",
			wantMinConf:    0.90,
		},
		{
			name:           "Correios SRO Internacional HK",
			code:           "LB445566778HK",
			wantCode:       "correios",
			wantName:       "Correios",
			want17ID:       2151,
			wantTMCode:     "brazil-correios",
			wantDetectedBy: "regex",
			wantMinConf:    0.90,
		},
		{
			name:           "Azul Cargo AZL demo",
			code:           "AZL577901234BR",
			wantCode:       "azul_express",
			wantName:       "Azul Cargo Express",
			want17ID:       190094,
			wantTMCode:     "azul-cargo",
			wantDetectedBy: "regex",
			wantMinConf:    0.90,
		},
		{
			name:           "Azul Cargo AWB 577 com hifen",
			code:           "577-12345678",
			wantCode:       "azul_express",
			wantName:       "Azul Cargo Express",
			want17ID:       190094,
			wantTMCode:     "azul-cargo",
			wantDetectedBy: "regex",
			wantMinConf:    0.90,
		},
		{
			name:           "Azul Cargo prefixo AZUL",
			code:           "AZUL99887766",
			wantCode:       "azul_express",
			wantName:       "Azul Cargo Express",
			want17ID:       190094,
			wantTMCode:     "azul-cargo",
			wantDetectedBy: "regex",
			wantMinConf:    0.90,
		},
		{
			name:           "Azul Cargo prefixo 800",
			code:           "80012345678",
			wantCode:       "azul_express",
			wantName:       "Azul Cargo Express",
			want17ID:       190094,
			wantTMCode:     "azul-cargo",
			wantDetectedBy: "regex",
			wantMinConf:    0.90,
		},
		{
			name:           "Loggi demo",
			code:           "LGG84920194BR",
			wantCode:       "loggi",
			wantName:       "Loggi",
			want17ID:       190271,
			wantTMCode:     "loggi",
			wantDetectedBy: "regex",
			wantMinConf:    0.90,
		},
		{
			name:           "Loggi prefixo LG",
			code:           "LG9876543210",
			wantCode:       "loggi",
			wantName:       "Loggi",
			want17ID:       190271,
			wantTMCode:     "loggi",
			wantDetectedBy: "regex",
			wantMinConf:    0.90,
		},
		{
			name:           "Mandae demo",
			code:           "MND994827162BR",
			wantCode:       "mandae",
			wantName:       "Mandaê",
			want17ID:       190412,
			wantTMCode:     "mandae",
			wantDetectedBy: "regex",
			wantMinConf:    0.90,
		},
		{
			name:           "Mandae numerico 99",
			code:           "991234567890",
			wantCode:       "mandae",
			wantName:       "Mandaê",
			want17ID:       190412,
			wantTMCode:     "mandae",
			wantDetectedBy: "regex",
			wantMinConf:    0.90,
		},
		{
			name:           "Magalog demo",
			code:           "MGL339104857BR",
			wantCode:       "magalog",
			wantName:       "Magalog (Magalu Entregas)",
			want17ID:       190518,
			wantTMCode:     "magalog",
			wantDetectedBy: "regex",
			wantMinConf:    0.90,
		},
		{
			name:           "Magalog prefixo MLG",
			code:           "MLG99887766",
			wantCode:       "magalog",
			wantName:       "Magalog (Magalu Entregas)",
			want17ID:       190518,
			wantTMCode:     "magalog",
			wantDetectedBy: "regex",
			wantMinConf:    0.90,
		},
		{
			name:           "Jadlog prefixo JAD",
			code:           "JAD1234567890",
			wantCode:       "jadlog",
			wantName:       "Jadlog",
			want17ID:       100457,
			wantTMCode:     "jadlog",
			wantDetectedBy: "regex",
			wantMinConf:    0.90,
		},
		{
			name:           "Jadlog 14 digitos",
			code:           "10081234567890",
			wantCode:       "jadlog",
			wantName:       "Jadlog",
			want17ID:       100457,
			wantTMCode:     "jadlog",
			wantDetectedBy: "regex",
			wantMinConf:    0.90,
		},
		{
			name:           "Total Express prefixo TEX",
			code:           "TEX1234567890",
			wantCode:       "total_express",
			wantName:       "Total Express",
			want17ID:       190152,
			wantTMCode:     "total-express",
			wantDetectedBy: "regex",
			wantMinConf:    0.90,
		},
		{
			name:           "Total Express numerico 7",
			code:           "712345678901",
			wantCode:       "total_express",
			wantName:       "Total Express",
			want17ID:       190152,
			wantTMCode:     "total-express",
			wantDetectedBy: "regex",
			wantMinConf:    0.90,
		},
		{
			name:           "Sequoia Shopee Xpress BR",
			code:           "BR2412345678901A",
			wantCode:       "sequoia",
			wantName:       "Sequoia / Shopee Xpress",
			want17ID:       190441,
			wantTMCode:     "sequoia",
			wantDetectedBy: "regex",
			wantMinConf:    0.90,
		},
		{
			name:           "Sequoia SEQ",
			code:           "SEQ1234567890",
			wantCode:       "sequoia",
			wantName:       "Sequoia / Shopee Xpress",
			want17ID:       190441,
			wantTMCode:     "sequoia",
			wantDetectedBy: "regex",
			wantMinConf:    0.90,
		},
		{
			name:           "Cainiao LP",
			code:           "LP00123456789012",
			wantCode:       "cainiao",
			wantName:       "Cainiao / AliExpress",
			want17ID:       190008,
			wantTMCode:     "cainiao",
			wantDetectedBy: "regex",
			wantMinConf:    0.90,
		},
		{
			name:           "JT Express prefixo JT",
			code:           "JT123456789012",
			wantCode:       "jt_express",
			wantName:       "J&T Express",
			want17ID:       100797,
			wantTMCode:     "jt-express",
			wantDetectedBy: "regex",
			wantMinConf:    0.90,
		},
		{
			name:           "JT Express prefixo 888 com 14 digitos",
			code:           "88800123456789",
			wantCode:       "jt_express",
			wantName:       "J&T Express",
			want17ID:       100797,
			wantTMCode:     "jt-express",
			wantDetectedBy: "regex",
			wantMinConf:    0.90,
		},
		{
			name:           "JT Express prefixo 888 com 15 digitos",
			code:           "888001234567890",
			wantCode:       "jt_express",
			wantName:       "J&T Express",
			want17ID:       100797,
			wantTMCode:     "jt-express",
			wantDetectedBy: "regex",
			wantMinConf:    0.90,
		},
		{
			name:           "Desconhecido fallback agregadora",
			code:           "CUSTOM-XYZ-991",
			wantCode:       "auto",
			wantName:       "Detecção automática (API Agregadora)",
			want17ID:       0,
			wantTMCode:     "",
			wantDetectedBy: "aggregator",
			wantExactConf:  0.35,
		},
		{
			name:           "Vazio fallback agregadora",
			code:           "   ",
			wantCode:       "auto",
			wantName:       "Detecção automática (API Agregadora)",
			want17ID:       0,
			wantTMCode:     "",
			wantDetectedBy: "aggregator",
			wantExactConf:  0.35,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectCarrier(tc.code)
			if got.Code != tc.wantCode {
				t.Errorf("Code = %q; want %q", got.Code, tc.wantCode)
			}
			if got.Name != tc.wantName {
				t.Errorf("Name = %q; want %q", got.Name, tc.wantName)
			}
			if got.Track17CarrierID != tc.want17ID {
				t.Errorf("Track17CarrierID = %d; want %d", got.Track17CarrierID, tc.want17ID)
			}
			if got.TrackingMoreCode != tc.wantTMCode {
				t.Errorf("TrackingMoreCode = %q; want %q", got.TrackingMoreCode, tc.wantTMCode)
			}
			if got.DetectedBy != tc.wantDetectedBy {
				t.Errorf("DetectedBy = %q; want %q", got.DetectedBy, tc.wantDetectedBy)
			}
			if tc.wantExactConf > 0 && got.Confidence != tc.wantExactConf {
				t.Errorf("Confidence = %v; want %v", got.Confidence, tc.wantExactConf)
			}
			if tc.wantMinConf > 0 && got.Confidence < tc.wantMinConf {
				t.Errorf("Confidence = %v; want >= %v", got.Confidence, tc.wantMinConf)
			}
			if got.PatternDescription == "" {
				t.Error("PatternDescription should not be empty")
			}
		})
	}
}

func TestListSupportedCarriers(t *testing.T) {
	carriers := ListSupportedCarriers()
	if len(carriers) < 9 {
		t.Fatalf("expected at least 9 supported carriers, got %d", len(carriers))
	}

	for _, c := range carriers {
		if c.Code == "" || c.Name == "" {
			t.Errorf("carrier missing code or name: %+v", c)
		}
		if c.RegexPattern == "" {
			t.Errorf("carrier %s missing RegexPattern", c.Code)
		}
		if c.PatternDescription == "" {
			t.Errorf("carrier %s missing PatternDescription", c.Code)
		}
		if len(c.SampleCodes) == 0 {
			t.Errorf("carrier %s missing SampleCodes", c.Code)
		}
		for _, sample := range c.SampleCodes {
			detected := DetectCarrier(sample)
			if detected.Code != c.Code {
				t.Errorf("sample code %q for %s detected as %q instead", sample, c.Code, detected.Code)
			}
		}
	}

	// Verifica que mutar a cópia retornada não afeta chamadas futuras.
	carriers[0].SampleCodes[0] = "MUTATED"
	again := ListSupportedCarriers()
	if again[0].SampleCodes[0] == "MUTATED" {
		t.Error("ListSupportedCarriers should return cloned SampleCodes slice")
	}
}

func TestNormalizeStatus(t *testing.T) {
	cases := []struct {
		raw      string
		wantStat domain.StandardStatus
		wantLbl  string
		wantStep int
	}{
		// Delivered
		{"Objeto entregue", domain.StatusDelivered, "Entregue", 4},
		{"ENTREGUE AO DESTINATÁRIO", domain.StatusDelivered, "Entregue", 4},
		{"entregue ao destinatario", domain.StatusDelivered, "Entregue", 4},
		{"Delivered", domain.StatusDelivered, "Entregue", 4},
		{"Entrega realizada", domain.StatusDelivered, "Entregue", 4},
		{"Pacote entregue", domain.StatusDelivered, "Entregue", 4},
		{"Finalizado com sucesso", domain.StatusDelivered, "Entregue", 4},
		{"Entregue", domain.StatusDelivered, "Entregue", 4},

		// Out for delivery
		{"Saiu para entrega", domain.StatusOutForDelivery, "Saiu para entrega", 3},
		{"Em rota de entrega", domain.StatusOutForDelivery, "Saiu para entrega", 3},
		{"Motorista a caminho", domain.StatusOutForDelivery, "Saiu para entrega", 3},
		{"Out for delivery", domain.StatusOutForDelivery, "Saiu para entrega", 3},
		{"OutForDelivery", domain.StatusOutForDelivery, "Saiu para entrega", 3},
		{"Em rota", domain.StatusOutForDelivery, "Saiu para entrega", 3},
		{"VEÍCULO EM DESLOCAMENTO", domain.StatusOutForDelivery, "Saiu para entrega", 3},
		{"Saiu para entrega ao destinatário", domain.StatusOutForDelivery, "Saiu para entrega", 3},

		// At CDD
		{"Aguardando retirada", domain.StatusAtCDD, "Alocado no CDD / Aguardando retirada", 3},
		{"Disponível para retirada", domain.StatusAtCDD, "Alocado no CDD / Aguardando retirada", 3},
		{"disponivel para retirada", domain.StatusAtCDD, "Alocado no CDD / Aguardando retirada", 3},
		{"Alocado no CDD", domain.StatusAtCDD, "Alocado no CDD / Aguardando retirada", 3},
		{"Objeto disponível na unidade", domain.StatusAtCDD, "Alocado no CDD / Aguardando retirada", 3},
		{"Chegou na unidade de distribuição", domain.StatusAtCDD, "Alocado no CDD / Aguardando retirada", 3},
		{"No centro de distribuição", domain.StatusAtCDD, "Alocado no CDD / Aguardando retirada", 3},
		{"Available for pickup", domain.StatusAtCDD, "Alocado no CDD / Aguardando retirada", 3},
		{"CDD", domain.StatusAtCDD, "Alocado no CDD / Aguardando retirada", 3},

		// Exception
		{"Endereço incorreto", domain.StatusException, "Ocorrência / Atenção", 2},
		{"Destinatário ausente", domain.StatusException, "Ocorrência / Atenção", 2},
		{"Tentativa de entrega não efetuada", domain.StatusException, "Ocorrência / Atenção", 2},
		{"Extraviado", domain.StatusException, "Ocorrência / Atenção", 2},
		{"Retido na fiscalização", domain.StatusException, "Ocorrência / Atenção", 2},
		{"Exception", domain.StatusException, "Ocorrência / Atenção", 2},
		{"Devolvido ao remetente", domain.StatusException, "Ocorrência / Atenção", 2},
		{"Pendência operacional", domain.StatusException, "Ocorrência / Atenção", 2},
		{"Não localizado", domain.StatusException, "Ocorrência / Atenção", 2},
		{"Objeto não entregue", domain.StatusException, "Ocorrência / Atenção", 2},
		{"Undelivered", domain.StatusException, "Ocorrência / Atenção", 2},

		// In Transit
		{"Objeto em trânsito", domain.StatusInTransit, "Em trânsito", 2},
		{"Em transferência", domain.StatusInTransit, "Em trânsito", 2},
		{"Encaminhado para CTE Curitiba", domain.StatusInTransit, "Em trânsito", 2},
		{"In transit", domain.StatusInTransit, "Em trânsito", 2},
		{"InTransit", domain.StatusInTransit, "Em trânsito", 2},
		{"Chegou ao hub Cajamar", domain.StatusInTransit, "Em trânsito", 2},
		{"Em triagem", domain.StatusInTransit, "Em trânsito", 2},
		{"Processado no centro logístico", domain.StatusInTransit, "Em trânsito", 2},
		{"Desembaraço aduaneiro", domain.StatusInTransit, "Em trânsito", 2},
		{"Em viagem", domain.StatusInTransit, "Em trânsito", 2},

		// Posted
		{"Objeto postado", domain.StatusPosted, "Postado", 1},
		{"Coletado pela transportadora", domain.StatusPosted, "Postado", 1},
		{"Etiqueta emitida", domain.StatusPosted, "Postado", 1},
		{"Nota fiscal emitida", domain.StatusPosted, "Postado", 1},
		{"Posted", domain.StatusPosted, "Postado", 1},
		{"Info received", domain.StatusPosted, "Postado", 1},
		{"InfoReceived", domain.StatusPosted, "Postado", 1},
		{"Aguardando coleta", domain.StatusPosted, "Postado", 1},
		{"Pedido recebido pela transportadora", domain.StatusPosted, "Postado", 1},

		// NFD combining marks
		{"Destinata\u0301rio ausente", domain.StatusException, "Ocorrência / Atenção", 2},
		{"Objeto em tra\u0302nsito", domain.StatusInTransit, "Em trânsito", 2},
	}

	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			stat, lbl, step := NormalizeStatus(tc.raw)
			if stat != tc.wantStat || lbl != tc.wantLbl || step != tc.wantStep {
				t.Errorf("NormalizeStatus(%q) = (%q, %q, %d); want (%q, %q, %d)",
					tc.raw, stat, lbl, step, tc.wantStat, tc.wantLbl, tc.wantStep)
			}
		})
	}
}
