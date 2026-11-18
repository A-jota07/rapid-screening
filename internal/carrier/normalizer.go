package carrier

import (
	"strings"
	"unicode"

	"rapid-screening/internal/domain"
)

type statusRule struct {
	status   domain.StandardStatus
	step     int
	keywords []string
}

var statusRules = []statusRule{
	{
		status: domain.StatusException,
		step:   2,
		keywords: []string{
			"endereco incorreto",
			"destinatario ausente",
			"tentativa de entrega nao efetuada",
			"tentativa de entrega",
			"entrega nao efetuada",
			"nao efetuada",
			"nao entregue",
			"undelivered",
			"delivery failure",
			"deliveryfailure",
			"delivery failed",
			"extraviado",
			"retido",
			"exception",
			"devolvido",
			"pendencia",
			"nao localizado",
			"recusado",
			"falha na entrega",
			"returned",
			"expired",
		},
	},
	{
		status: domain.StatusDelivered,
		step:   4,
		keywords: []string{
			"objeto entregue",
			"entregue ao destinatario",
			"entrega realizada",
			"pacote entregue",
			"finalizado com sucesso",
			"delivered",
			"entregue",
		},
	},
	{
		status: domain.StatusOutForDelivery,
		step:   3,
		keywords: []string{
			"saiu para entrega ao destinatario",
			"saiu para entrega",
			"em rota de entrega",
			"motorista a caminho",
			"out for delivery",
			"outfordelivery",
			"veiculo em deslocamento",
			"em rota",
		},
	},
	{
		status: domain.StatusAtCDD,
		step:   3,
		keywords: []string{
			"aguardando retirada",
			"disponivel para retirada",
			"alocado no cdd",
			"objeto disponivel na unidade",
			"chegou na unidade de distribuicao",
			"no centro de distribuicao",
			"available for pickup",
			"availableforpickup",
			"ready for pickup",
			"at cdd",
		},
	},
	{
		status: domain.StatusInTransit,
		step:   2,
		keywords: []string{
			"objeto em transito",
			"em transferencia",
			"encaminhado para",
			"in transit",
			"intransit",
			"chegou ao hub",
			"em triagem",
			"processado no centro logistico",
			"desembaraco aduaneiro",
			"em viagem",
			"em transito",
		},
	},
	{
		status:   domain.StatusAtCDD,
		step:     3,
		keywords: []string{"cdd"},
	},
	{
		status: domain.StatusPosted,
		step:   1,
		keywords: []string{
			"objeto postado",
			"coletado",
			"etiqueta emitida",
			"nota fiscal emitida",
			"posted",
			"info received",
			"inforeceived",
			"aguardando coleta",
			"pedido recebido pela transportadora",
			"postado",
		},
	},
}

// NormalizeStatus normaliza mensagens brutas (PT/EN, case-insensitive e tolerante a acentos)
// retornando o StandardStatus padronizado, seu rótulo amigável e o passo da barra de progresso (1..4).
func NormalizeStatus(rawStatus string) (domain.StandardStatus, string, int) {
	norm := normalizeText(rawStatus)
	if norm == "" {
		return domain.StatusInTransit, domain.StatusLabel(domain.StatusInTransit), 2
	}

	for _, rule := range statusRules {
		for _, kw := range rule.keywords {
			if kw == "cdd" {
				if containsWord(norm, "cdd") {
					return rule.status, domain.StatusLabel(rule.status), rule.step
				}
				continue
			}
			if strings.Contains(norm, kw) {
				return rule.status, domain.StatusLabel(rule.status), rule.step
			}
		}
	}

	return domain.StatusInTransit, domain.StatusLabel(domain.StatusInTransit), 2
}

func containsWord(text, word string) bool {
	for _, token := range strings.Fields(text) {
		if token == word {
			return true
		}
	}
	return false
}

// normalizeText converte para minúsculas, remove acentos (NFC e NFD) e normaliza separadores em espaços simples.
func normalizeText(s string) string {
	lower := strings.ToLower(strings.TrimSpace(s))
	if lower == "" {
		return ""
	}

	var b strings.Builder
	b.Grow(len(lower))
	prevSpace := false

	for _, r := range lower {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		mapped := stripAccentRune(r)
		if unicode.IsSpace(mapped) || mapped == '_' || mapped == '-' || mapped == '/' || mapped == '.' || mapped == ',' || mapped == ':' || mapped == ';' {
			if !prevSpace && b.Len() > 0 {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		b.WriteRune(mapped)
		prevSpace = false
	}

	return strings.TrimSpace(b.String())
}

func stripAccentRune(r rune) rune {
	switch r {
	case 'á', 'à', 'â', 'ã', 'ä', 'å':
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
