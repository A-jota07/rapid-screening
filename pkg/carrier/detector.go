package carrier

import (
	"regexp"
	"slices"
	"strings"
	"unicode"

	"rapid-screening/pkg/domain"
)

type carrierRule struct {
	info        domain.CarrierInfo
	strictRegex *regexp.Regexp
}

// Regras compiladas uma única vez na inicialização do pacote.
var (
	reCorreios    = regexp.MustCompile(`^[A-Z]{2}\d{9}[A-Z]{2}$`)
	reAzulExpress = regexp.MustCompile(`^(AZ|AZL|800)\d{8,11}[A-Z0-9]{0,2}$|^AZUL[A-Z0-9]{6,12}$|^577\d{8}$`)
	reLoggi       = regexp.MustCompile(`^(LGG|LOG|LG)[A-Z0-9]{8,14}$`)
	reMandae      = regexp.MustCompile(`^(MND|MAN|MD)[A-Z0-9]{7,13}$|^99\d{9,11}$`)
	reMagalog     = regexp.MustCompile(`^(MGL|MAG|MLG)[A-Z0-9]{8,14}$`)
	reJadlogPref  = regexp.MustCompile(`^(JAD|JD)\d{9,14}$`)
	reJadlogNum14 = regexp.MustCompile(`^\d{14}$`)
	reTotalExp    = regexp.MustCompile(`^(TEX|TOT)[A-Z0-9]{8,13}$|^7\d{10,12}$`)
	reSequoia     = regexp.MustCompile(`^(BR\d{12,13}[A-Z0-9]?|SEQ[A-Z0-9]{8,12})$`)
	reCainiao     = regexp.MustCompile(`^LP\d{12,15}$`)
	reJTExpress   = regexp.MustCompile(`^(JT|888)\d{10,14}$`)
	reAllDigits   = regexp.MustCompile(`^\d+$`)
)

var supportedCarriers = []domain.CarrierInfo{
	{
		Code:               "correios",
		Name:               "Correios",
		Track17CarrierID:   2151,
		TrackingMoreCode:   "brazil-correios",
		DetectedBy:         "regex",
		Confidence:         0.99,
		PatternDescription: "Padrão S10 UPU: 2 letras iniciais + 9 dígitos + 2 letras do país (ex: AA123456789BR)",
		RegexPattern:       `^[A-Z]{2}\d{9}[A-Z]{2}$`,
		SampleCodes:        []string{"AA123456789BR", "NL987654321BR", "LB445566778HK"},
	},
	{
		Code:               "azul_express",
		Name:               "Azul Cargo Express",
		Track17CarrierID:   190094,
		TrackingMoreCode:   "azul-cargo",
		DetectedBy:         "regex",
		Confidence:         0.97,
		PatternDescription: "AWB aéreo 577 + 8 dígitos ou prefixos AZ/AZL/AZUL/800",
		RegexPattern:       `^(AZ|AZL|800)\d{8,11}[A-Z0-9]{0,2}$|^AZUL[A-Z0-9]{6,12}$|^577\d{8}$`,
		SampleCodes:        []string{"AZL577901234BR", "57712345678", "AZUL12345678", "80012345678"},
	},
	{
		Code:               "loggi",
		Name:               "Loggi",
		Track17CarrierID:   190271,
		TrackingMoreCode:   "loggi",
		DetectedBy:         "regex",
		Confidence:         0.97,
		PatternDescription: "Prefixos LGG, LOG ou LG seguidos de 8 a 14 caracteres alfanuméricos",
		RegexPattern:       `^(LGG|LOG|LG)[A-Z0-9]{8,14}$`,
		SampleCodes:        []string{"LGG84920194BR", "LOG12345678BR", "LG9876543210"},
	},
	{
		Code:               "mandae",
		Name:               "Mandaê",
		Track17CarrierID:   190412,
		TrackingMoreCode:   "mandae",
		DetectedBy:         "regex",
		Confidence:         0.96,
		PatternDescription: "Prefixos MND, MAN, MD com 7-13 caracteres ou numérico iniciado por 99",
		RegexPattern:       `^(MND|MAN|MD)[A-Z0-9]{7,13}$|^99\d{9,11}$`,
		SampleCodes:        []string{"MND994827162BR", "MAN12345678", "99123456789"},
	},
	{
		Code:               "magalog",
		Name:               "Magalog (Magalu Entregas)",
		Track17CarrierID:   190518,
		TrackingMoreCode:   "magalog",
		DetectedBy:         "regex",
		Confidence:         0.97,
		PatternDescription: "Prefixos MGL, MAG ou MLG seguidos de 8 a 14 caracteres alfanuméricos",
		RegexPattern:       `^(MGL|MAG|MLG)[A-Z0-9]{8,14}$`,
		SampleCodes:        []string{"MGL339104857BR", "MAG123456789", "MLG99887766"},
	},
	{
		Code:               "jadlog",
		Name:               "Jadlog",
		Track17CarrierID:   100457,
		TrackingMoreCode:   "jadlog",
		DetectedBy:         "regex",
		Confidence:         0.95,
		PatternDescription: "Prefixo JAD/JD seguido de 9 a 14 dígitos ou código numérico de remessa com 14 dígitos",
		RegexPattern:       `^(JAD|JD)\d{9,14}$|^\d{14}$`,
		SampleCodes:        []string{"JAD1234567890", "JD987654321", "10081234567890"},
	},
	{
		Code:               "total_express",
		Name:               "Total Express",
		Track17CarrierID:   190152,
		TrackingMoreCode:   "total-express",
		DetectedBy:         "regex",
		Confidence:         0.95,
		PatternDescription: "Prefixos TEX ou TOT seguidos de 8-13 caracteres ou numérico iniciado por 7 (11-13 dígitos)",
		RegexPattern:       `^(TEX|TOT)[A-Z0-9]{8,13}$|^7\d{10,12}$`,
		SampleCodes:        []string{"TEX1234567890", "TOT9988776655", "712345678901"},
	},
	{
		Code:               "sequoia",
		Name:               "Sequoia / Shopee Xpress",
		Track17CarrierID:   190441,
		TrackingMoreCode:   "sequoia",
		DetectedBy:         "regex",
		Confidence:         0.96,
		PatternDescription: "Padrão Shopee Xpress BR + 12-13 dígitos (com sufixo opcional) ou Sequoia SEQ + 8-12 caracteres",
		RegexPattern:       `^(BR\d{12,13}[A-Z0-9]?|SEQ[A-Z0-9]{8,12})$`,
		SampleCodes:        []string{"BR2412345678901", "BR231234567890A", "SEQ1234567890"},
	},
	{
		Code:               "cainiao",
		Name:               "Cainiao / AliExpress",
		Track17CarrierID:   190008,
		TrackingMoreCode:   "cainiao",
		DetectedBy:         "regex",
		Confidence:         0.97,
		PatternDescription: "Prefixo internacional LP seguido de 12 a 15 dígitos (Cainiao / AliExpress)",
		RegexPattern:       `^LP\d{12,15}$`,
		SampleCodes:        []string{"LP00123456789012", "LP99887766554433"},
	},
	{
		Code:               "jt_express",
		Name:               "J&T Express",
		Track17CarrierID:   100797,
		TrackingMoreCode:   "jt-express",
		DetectedBy:         "regex",
		Confidence:         0.96,
		PatternDescription: "Prefixos JT ou 888 seguidos de 10 a 14 dígitos",
		RegexPattern:       `^(JT|888)\d{10,14}$`,
		SampleCodes:        []string{"JT123456789012", "888001234567890"},
	},
}

// Ordem de avaliação garante prioridade a prefixos explícitos antes de padrões puramente numéricos (ex: 14 dígitos da Jadlog).
var detectionRules = []carrierRule{
	{info: supportedCarriers[0], strictRegex: reCorreios},
	{info: supportedCarriers[1], strictRegex: reAzulExpress},
	{info: supportedCarriers[2], strictRegex: reLoggi},
	{info: supportedCarriers[3], strictRegex: reMandae},
	{info: supportedCarriers[4], strictRegex: reMagalog},
	{info: supportedCarriers[5], strictRegex: reJadlogPref},
	{info: supportedCarriers[6], strictRegex: reTotalExp},
	{info: supportedCarriers[7], strictRegex: reSequoia},
	{info: supportedCarriers[8], strictRegex: reCainiao},
	{info: supportedCarriers[9], strictRegex: reJTExpress},
	{info: supportedCarriers[5], strictRegex: reJadlogNum14},
}

// SanitizeCode remove espaços (qualquer whitespace), hífens e converte o código para maiúsculas.
func SanitizeCode(code string) string {
	var b strings.Builder
	b.Grow(len(code))
	for _, r := range code {
		if unicode.IsSpace(r) || r == '-' {
			continue
		}
		b.WriteRune(r)
	}
	return strings.ToUpper(b.String())
}

// DetectCarrier identifica a transportadora mais provável para o código informado usando Regex prioritário.
// Quando não há correspondência estrita, retorna o contrato de detecção automática via API agregadora.
func DetectCarrier(code string) domain.CarrierInfo {
	clean := SanitizeCode(code)
	if clean == "" {
		return domain.CarrierInfo{
			Code:               "auto",
			Name:               "Detecção automática (API Agregadora)",
			DetectedBy:         "aggregator",
			Confidence:         0.35,
			PatternDescription: "Código vazio ou inválido; utilizando detecção automática pela API agregadora",
		}
	}

	for _, rule := range detectionRules {
		if rule.strictRegex.MatchString(clean) {
			return cloneCarrierInfo(rule.info)
		}
	}

	hint := buildHeuristicHint(clean)
	return domain.CarrierInfo{
		Code:               "auto",
		Name:               "Detecção automática (API Agregadora)",
		DetectedBy:         "aggregator",
		Confidence:         0.35,
		PatternDescription: hint,
	}
}

// ListSupportedCarriers retorna a lista de todas as transportadoras suportadas com Regex e códigos de exemplo.
func ListSupportedCarriers() []domain.CarrierInfo {
	out := make([]domain.CarrierInfo, len(supportedCarriers))
	for i, c := range supportedCarriers {
		out[i] = cloneCarrierInfo(c)
	}
	return out
}

func cloneCarrierInfo(c domain.CarrierInfo) domain.CarrierInfo {
	c.SampleCodes = slices.Clone(c.SampleCodes)
	return c
}

func buildHeuristicHint(clean string) string {
	switch {
	case reAllDigits.MatchString(clean) && len(clean) >= 8:
		return "Sugestão heurística: código numérico regional ou CT-e; será consultado via API agregadora automática"
	case len(clean) >= 10 && strings.HasPrefix(clean, "BR"):
		return "Sugestão heurística: possível remessa doméstica/cross-border BR; detecção via API agregadora"
	case len(clean) >= 10 && unicode.IsLetter(rune(clean[0])) && unicode.IsLetter(rune(clean[1])):
		return "Sugestão heurística: código alfanumérico fora do padrão S10 estrito; detecção via API agregadora"
	default:
		return "Formato não catalogado em Regex estrito; detecção automática via API agregadora"
	}
}
