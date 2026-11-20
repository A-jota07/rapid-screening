package providers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	carrierpkg "rapid-screening/pkg/carrier"
	"rapid-screening/pkg/domain"
)

// ProviderChain orquestra múltiplos provedores em cascata:
// free_direct -> siterastreio (1.000 grátis/mês) -> 17track -> trackingmore (-> smart_demo_fallback se DEMO_FALLBACK_ENABLED=true).
type ProviderChain struct {
	providers       []Provider
	allowSampleDemo bool
}

// NewDefaultChain instancia a cadeia padrão lendo as variáveis de ambiente de configuração e créditos.
// Em produção (DEMO_FALLBACK_ENABLED != "true"), não inclui dados simulados para códigos reais,
// mantendo funcionando apenas os 5 chips explícitos de exemplo da interface.
func NewDefaultChain() *ProviderChain {
	list := []Provider{
		NewFreeDirectProviderFromEnv(),
		NewSiteRastreioProviderFromEnv(),
		NewTrack17ProviderFromEnv(),
		NewTrackingMoreProviderFromEnv(),
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("DEMO_FALLBACK_ENABLED")), "true") {
		list = append(list, NewSmartFallbackProvider())
	}
	c := NewChain(list)
	c.allowSampleDemo = true
	return c
}

// NewChain cria uma cadeia personalizada com os provedores fornecidos na ordem de prioridade.
func NewChain(providers []Provider) *ProviderChain {
	filtered := make([]Provider, 0, len(providers))
	for _, p := range providers {
		if p != nil {
			filtered = append(filtered, p)
		}
	}
	return &ProviderChain{providers: filtered}
}

// Track consulta os provedores em ordem até obter um resultado válido, acionando fallback imediato em falhas ou falta de créditos.
func (c *ProviderChain) Track(ctx context.Context, code string, carrierInfo domain.CarrierInfo) (*domain.TrackingResult, error) {
	cleanCode := carrierpkg.SanitizeCode(code)
	if cleanCode == "" {
		return nil, fmt.Errorf("%w: tracking code cannot be empty", ErrNotFound)
	}

	if carrierInfo.Code == "" {
		carrierInfo = carrierpkg.DetectCarrier(cleanCode)
	}

	if len(c.providers) == 0 {
		return nil, fmt.Errorf("%w: no providers configured in chain", ErrUnavailable)
	}

	errs := make([]error, 0, len(c.providers))
	for _, p := range c.providers {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		res, err := p.Track(ctx, cleanCode, carrierInfo)
		if err == nil && res != nil {
			res.TrackingCode = cleanCode
			if res.ProviderUsed == "" {
				res.ProviderUsed = p.Name()
			}
			return res, nil
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p.Name(), err))
		} else {
			errs = append(errs, fmt.Errorf("%s: returned nil result", p.Name()))
		}
	}

	if c.allowSampleDemo && IsExplicitSampleCode(cleanCode) {
		if res, err := NewSmartFallbackProvider().Track(ctx, cleanCode, carrierInfo); err == nil && res != nil {
			res.TrackingCode = cleanCode
			return res, nil
		}
	}

	return nil, errors.Join(errs...)
}

// IsExplicitSampleCode identifica os 5 códigos de exemplo exibidos nos chips da tela inicial.
func IsExplicitSampleCode(code string) bool {
	switch carrierpkg.SanitizeCode(code) {
	case "AA123456789BR", "LGG84920194BR", "AZL577901234BR", "MND994827162BR", "MGL339104857BR":
		return true
	default:
		return false
	}
}

// Statuses retorna o estado de saúde, créditos e circuit-breaker de cada provedor da cadeia.
func (c *ProviderChain) Statuses() []domain.ProviderHealth {
	out := make([]domain.ProviderHealth, len(c.providers))
	for i, p := range c.providers {
		out[i] = p.Status()
	}
	return out
}
