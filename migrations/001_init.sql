-- =============================================================================
-- Rapid Screening - Schema Inicial PostgreSQL 16
-- Tabelas: cdd_locations, trackings, tracking_events, provider_credit_logs
-- =============================================================================

BEGIN;

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- Função auxiliar para manter updated_at sincronizado automaticamente.
CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- -----------------------------------------------------------------------------
-- 1. Catálogo de Hubs e CDDs (Centros de Distribuição Domiciliária) no Brasil
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS cdd_locations (
    id              BIGSERIAL PRIMARY KEY,
    external_id     VARCHAR(80) UNIQUE NOT NULL,
    name            VARCHAR(180) NOT NULL,
    carrier_code    VARCHAR(60) NOT NULL,
    carrier_name    VARCHAR(120) NOT NULL,
    facility_type   VARCHAR(20) NOT NULL DEFAULT 'CDD' CHECK (facility_type IN ('HUB', 'CDD', 'ORIGIN', 'DESTINATION')),
    neighborhood    VARCHAR(120) NOT NULL DEFAULT '',
    city            VARCHAR(120) NOT NULL,
    state           CHAR(2) NOT NULL,
    address         TEXT NOT NULL,
    zip_code        VARCHAR(16) NOT NULL DEFAULT '',
    phone           VARCHAR(40) NOT NULL DEFAULT '',
    opening_hours   VARCHAR(160) NOT NULL DEFAULT '',
    lat             DOUBLE PRECISION NOT NULL,
    lng             DOUBLE PRECISION NOT NULL,
    instructions    TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cdd_locations_city_state
    ON cdd_locations (LOWER(city), state);

CREATE INDEX IF NOT EXISTS idx_cdd_locations_carrier_code
    ON cdd_locations (carrier_code);

CREATE TRIGGER trg_cdd_locations_updated_at
    BEFORE UPDATE ON cdd_locations
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- -----------------------------------------------------------------------------
-- 2. Rastreamentos consolidados e estado de cache
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS trackings (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tracking_code         VARCHAR(64) UNIQUE NOT NULL,
    carrier_code          VARCHAR(60) NOT NULL DEFAULT 'auto',
    carrier_name          VARCHAR(120) NOT NULL DEFAULT '',
    track17_carrier_id    INTEGER NOT NULL DEFAULT 0,
    trackingmore_code     VARCHAR(80) NOT NULL DEFAULT '',
    detected_by           VARCHAR(32) NOT NULL DEFAULT 'regex',
    carrier_confidence    DOUBLE PRECISION NOT NULL DEFAULT 0,
    current_status        VARCHAR(32) NOT NULL DEFAULT 'POSTED'
                          CHECK (current_status IN ('POSTED', 'IN_TRANSIT', 'AT_CDD', 'OUT_FOR_DELIVERY', 'DELIVERED', 'EXCEPTION')),
    current_status_label  VARCHAR(120) NOT NULL DEFAULT '',
    progress_step         SMALLINT NOT NULL DEFAULT 1 CHECK (progress_step BETWEEN 1 AND 4),
    estimated_delivery    VARCHAR(64) NOT NULL DEFAULT '',
    origin_json           JSONB,
    destination_json      JSONB,
    current_location_json JSONB,
    cdd_id                BIGINT REFERENCES cdd_locations(id) ON DELETE SET NULL,
    cdd_json              JSONB,
    route_points          JSONB NOT NULL DEFAULT '[]'::jsonb,
    provider_used         VARCHAR(64) NOT NULL DEFAULT '',
    cache_expires_at      TIMESTAMPTZ,
    last_fetched_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_trackings_code
    ON trackings (tracking_code);

CREATE INDEX IF NOT EXISTS idx_trackings_status_updated
    ON trackings (current_status, last_updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_trackings_last_updated
    ON trackings (last_updated_at DESC);

CREATE TRIGGER trg_trackings_updated_at
    BEFORE UPDATE ON trackings
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- -----------------------------------------------------------------------------
-- 3. Histórico cronológico de eventos por encomenda
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS tracking_events (
    id               BIGSERIAL PRIMARY KEY,
    tracking_id      UUID NOT NULL REFERENCES trackings(id) ON DELETE CASCADE,
    event_timestamp  TIMESTAMPTZ NOT NULL,
    raw_status       TEXT NOT NULL DEFAULT '',
    standard_status  VARCHAR(32) NOT NULL
                     CHECK (standard_status IN ('POSTED', 'IN_TRANSIT', 'AT_CDD', 'OUT_FOR_DELIVERY', 'DELIVERED', 'EXCEPTION')),
    standard_label   VARCHAR(120) NOT NULL DEFAULT '',
    description      TEXT NOT NULL DEFAULT '',
    location         VARCHAR(255) NOT NULL DEFAULT '',
    lat              DOUBLE PRECISION,
    lng              DOUBLE PRECISION,
    facility_type    VARCHAR(20) NOT NULL DEFAULT '',
    facility_name    VARCHAR(180) NOT NULL DEFAULT '',
    event_hash       CHAR(64) NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_tracking_events_hash UNIQUE (tracking_id, event_hash)
);

CREATE INDEX IF NOT EXISTS idx_tracking_events_timeline
    ON tracking_events (tracking_id, event_timestamp DESC);

-- -----------------------------------------------------------------------------
-- 4. Auditoria de consumo e economia de créditos de provedores externos
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS provider_credit_logs (
    id               BIGSERIAL PRIMARY KEY,
    tracking_code    VARCHAR(64) NOT NULL,
    provider_name    VARCHAR(64) NOT NULL,
    action           VARCHAR(32) NOT NULL CHECK (action IN ('track', 'register', 'webhook', 'cache_hit', 'cooldown_hit')),
    credits_consumed INTEGER NOT NULL DEFAULT 0,
    credits_saved    INTEGER NOT NULL DEFAULT 0,
    cache_hit        BOOLEAN NOT NULL DEFAULT FALSE,
    latency_ms       INTEGER NOT NULL DEFAULT 0,
    http_status      INTEGER NOT NULL DEFAULT 200,
    success          BOOLEAN NOT NULL DEFAULT TRUE,
    error_message    TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_provider_credit_logs_provider_created
    ON provider_credit_logs (provider_name, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_provider_credit_logs_code
    ON provider_credit_logs (tracking_code, created_at DESC);

COMMIT;
