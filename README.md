# Rapid Screening — Rastreamento Multi-Transportadoras com Mapa CDD & GPS

Sistema full-stack gratuito de rastreamento de encomendas construído com **Go** (stdlib `net/http` + Serverless para Vercel) e **React + TypeScript + Tailwind CSS + Leaflet**, projetado para operar com **custo zero** tanto localmente quanto publicado na **Vercel**.

---

## Principais Recursos

### 1. Backend em Go Seguro, Modular e Anti-Desperdício de Créditos
- **Chaves de API 100% protegidas no backend**: o frontend nunca acessa provedores externos diretamente; todas as requisições passam por `/api/v1/...`.
- **Detecção automática de transportadora via Regex (`pkg/carrier/detector.go`)**:
  - **Correios** (`^[A-Z]{2}\d{9}[A-Z]{2}$` — ex: `AA123456789BR`, `NL987654321BR`)
  - **Azul Cargo Express** (`^(AZ|AZL|800)\d{8,11}[A-Z0-9]{0,2}$`, `^AZUL[A-Z0-9]{6,12}$`, `^577\d{8}$`)
  - **Loggi** (`^(LGG|LOG|LG)[A-Z0-9]{8,14}$`)
  - **Mandaê** (`^(MND|MAN|MD)[A-Z0-9]{7,13}$`, `^99\d{9,11}$`)
  - **Magalog (Magalu Entregas)** (`^(MGL|MAG|MLG)[A-Z0-9]{8,14}$`)
  - **Jadlog** (`^(JAD|JD)\d{9,14}$`, `^\d{14}$`)
  - **Total Express** (`^(TEX|TOT)[A-Z0-9]{8,13}$`, `^7\d{10,12}$`)
  - **Sequoia / Shopee Xpress / Cainiao / J&T Express**
  - Códigos fora dos padrões acima são marcados como `auto` para detecção pela API agregadora.
- **Padronização de Status (`pkg/carrier/normalizer.go`)**:
  Traduz mensagens heterogêneas (PT/EN) para estados fixos:
  `Postado` → `Em trânsito` → `Alocado no CDD / Aguardando retirada` / `Saiu para entrega` → `Entregue` (além de `Ocorrência / Atenção`).
- **Cadeia Multi-Provedor com Fallback e Circuit-Breaker (`pkg/providers/chain.go`)**:
  1. `free_direct` — Consulta gratuita direta quando disponível (`FREE_DIRECT_ENABLED=true`).
  2. `siterastreio` — **API Site Rastreio / Seu Rastreio** (`SITERASTREIO_API_KEY`) com **1.000 requisições gratuitas por mês** renováveis e crédito de rodapé automático no frontend.
  3. `17track` — **17TRACK API v2.2** (`TRACK17_API_KEY`) com controle de créditos e abertura automática de circuit-breaker em HTTP 429 / cota esgotada.
  4. `trackingmore` — **TrackingMore API v4** (`TRACKINGMORE_API_KEY`) como failover imediato quando os anteriores falharem ou ficarem sem créditos.
  5. `smart_demo_fallback` — Fallback determinístico gratuito com rotas e CDDs brasileiros reais, garantindo funcionamento mesmo sem chaves de API ou após o esgotamento de cotas gratuitas.
- **Cache em Múltiplas Camadas + Webhooks**:
  - **Edge CDN da Vercel**: `Cache-Control: public, s-maxage=300, stale-while-revalidate=600` em `GET /api/v1/track/{code}` (desativado em `?refresh=true`).
  - **Cache L1 em memória** com TTL dinâmico (`15 min` em trânsito, `10 min` no CDD/em rota, `24h` entregue) + **cooldown anti-abuso** de 60s.
  - **Persistência L2**: arquivo JSON atômico (`data/trackings.json` local ou `/tmp/rapid_screening_trackings.json` na Vercel) e integração opcional via REST com **Upstash Redis / Vercel KV** (`UPSTASH_REDIS_REST_URL` + `UPSTASH_REDIS_REST_TOKEN`).
  - **Webhooks**: `POST /api/v1/webhooks/17track` e `POST /api/v1/webhooks/trackingmore` atualizam o cache/banco imediatamente via push sem gastar consultas de polling.

### 2. Frontend em Paleta Natural + Mapa Leaflet + GPS
- **Paleta orgânica confortável (sem neon)**: linho/areia (`#F6F4EE`), branco quente (`#FCFBF8`), verde-sálvia/floresta (`#2E5A44`), terracota (`#B85D3B`) e âmbar terroso (`#C47D2B`).
- **Mapa interativo Leaflet (`frontend/src/components/TrackingMap.tsx`)**:
  - Polilinha da rota completa (`Origem` → `Hub` → `CDD` → `Destino`).
  - Marcador destacado do **CDD (Centro de Distribuição Domiciliária)** com endereço, CEP, telefone, horário de funcionamento e instruções de retirada.
  - **Navegação GPS**: botão para capturar sua localização (`navigator.geolocation`), cálculo de distância (Haversine) e tempo estimado a pé/de carro até o CDD, linha tracejada no mapa (com simulação próxima opcional para testes) e atalhos diretos para **Google Maps**, **Waze** e **OpenStreetMap**.

---

## Estrutura do Repositório

```text
rapid-screening/
├── api/
│   ├── index.go             # Entrypoint Vercel Go Serverless Function
│   └── index_test.go
├── cmd/server/main.go       # Servidor HTTP local para desenvolvimento/Docker
├── pkg/
│   ├── api/                 # Handlers REST, middlewares (CORS, rate-limit) e webhooks
│   ├── carrier/             # Regex de transportadoras e normalizador de status
│   ├── domain/              # Modelos canônicos compartilhados
│   ├── geo/                 # Catálogo de Hubs e CDDs brasileiros
│   ├── providers/           # FreeDirect, 17TRACK, TrackingMore, SmartFallback e Chain
│   ├── store/               # Cache L1, persistência JSON /tmp e Upstash Redis REST
│   └── verceladapter/       # Handler compartilhado para Vercel Serverless Functions
├── frontend/                # Aplicação React + TypeScript + Vite + Tailwind + Leaflet
├── migrations/001_init.sql  # Schema PostgreSQL para uso opcional com Docker/RDS
├── docker-compose.yml       # PostgreSQL 16 + Redis 7 + serviço Go opcional
├── go.mod
├── vercel.json              # Build do Vite + rewrites /api/(v1/.*) -> /api/index.go
└── .env.example
```

---

## Rodando Localmente

### 1. Backend Go
```bash
cp .env.example .env
go run ./cmd/server
```
O servidor sobe em `http://localhost:8080`.

### 2. Frontend React + Vite
```bash
cd frontend
npm install
npm run dev
```
Acesse `http://localhost:5173`. O proxy do Vite redireciona `/api` automaticamente para `http://localhost:8080`.

---

## Deploy Gratuito na Vercel

O projeto já está configurado com `vercel.json`, `go.mod` na raiz e `api/index.go`:

1. Suba o repositório para o GitHub/GitLab/Bitbucket e importe-o na **Vercel** (Plano Hobby gratuito).
2. A Vercel usará automaticamente as regras de `vercel.json`:
   - `buildCommand`: `cd frontend && npm install && npm run build`
   - `outputDirectory`: `frontend/dist`
   - `/api/v1/*` roteado para a função Go Serverless em `api/index.go`
   - Demais rotas roteadas para `/index.html` (SPA)
3. *(Opcional)* Configure variáveis de ambiente no painel da Vercel (**Settings → Environment Variables**):
   - `TRACK17_API_KEY`: sua chave gratuita da 17TRACK
   - `TRACKINGMORE_API_KEY`: sua chave gratuita da TrackingMore (fallback)
   - `UPSTASH_REDIS_REST_URL` e `UPSTASH_REDIS_REST_TOKEN` (ou `KV_REST_API_URL` / `KV_REST_API_TOKEN`): para manter cache compartilhado entre cold starts na Vercel usando o tier gratuito do Upstash/Vercel KV
   - `WEBHOOK_SECRET`: segredo para validar webhooks em `https://seu-dominio.vercel.app/api/v1/webhooks/17track` e `/api/v1/webhooks/trackingmore`

> Sem nenhuma variável configurada, a aplicação continua 100% funcional no modo gratuito usando detecção por Regex, rotas/CDDs brasileiros reais, fallback inteligente e cache na borda da Vercel.

---

## Testes

```bash
# Backend (inclui detecção Regex, normalização, cadeia multi-API, store/Upstash, API e entrypoint Vercel)
go test -v -race ./...
go vet ./...

# Frontend
cd frontend && npm run build
```
