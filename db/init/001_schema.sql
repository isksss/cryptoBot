CREATE TABLE job_runs (
    id BIGSERIAL PRIMARY KEY,
    job_type TEXT NOT NULL CHECK (job_type IN ('price_fetch', 'daily_trade', 'order_reconcile', 'manual')),
    status TEXT NOT NULL CHECK (status IN ('running', 'succeeded', 'failed', 'skipped')),
    scheduled_for TIMESTAMPTZ NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ,
    error_code TEXT,
    error_message TEXT,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_job_runs_job_type_scheduled_for
    ON job_runs (job_type, scheduled_for DESC);

CREATE TABLE price_snapshots (
    id BIGSERIAL PRIMARY KEY,
    asset_code TEXT NOT NULL CHECK (asset_code IN ('BTC', 'ETH')),
    price_jpy NUMERIC(20, 8) NOT NULL CHECK (price_jpy > 0),
    captured_at TIMESTAMPTZ NOT NULL,
    source TEXT NOT NULL DEFAULT 'gmo_public_api',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_price_snapshots_asset_code_captured_at
    ON price_snapshots (asset_code, captured_at DESC);

CREATE TABLE balance_snapshots (
    id BIGSERIAL PRIMARY KEY,
    job_run_id BIGINT REFERENCES job_runs (id) ON DELETE SET NULL,
    asset_code TEXT NOT NULL CHECK (asset_code IN ('JPY', 'BTC', 'ETH')),
    available_amount NUMERIC(20, 8) NOT NULL DEFAULT 0 CHECK (available_amount >= 0),
    locked_amount NUMERIC(20, 8) NOT NULL DEFAULT 0 CHECK (locked_amount >= 0),
    captured_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_balance_snapshots_asset_code_captured_at
    ON balance_snapshots (asset_code, captured_at DESC);

CREATE TABLE orders (
    id BIGSERIAL PRIMARY KEY,
    job_run_id BIGINT REFERENCES job_runs (id) ON DELETE SET NULL,
    exchange_order_id TEXT NOT NULL UNIQUE,
    client_order_id UUID NOT NULL UNIQUE,
    asset_code TEXT NOT NULL CHECK (asset_code IN ('BTC', 'ETH')),
    side TEXT NOT NULL CHECK (side IN ('buy', 'sell')),
    order_type TEXT NOT NULL CHECK (order_type IN ('limit')),
    status TEXT NOT NULL CHECK (
        status IN (
            'pending_submit',
            'open',
            'partially_filled',
            'filled',
            'cancel_requested',
            'cancelled',
            'expired',
            'rejected',
            'failed'
        )
    ),
    price_jpy NUMERIC(20, 8) NOT NULL CHECK (price_jpy > 0),
    ordered_units NUMERIC(20, 8) NOT NULL CHECK (ordered_units > 0),
    filled_units NUMERIC(20, 8) NOT NULL DEFAULT 0 CHECK (filled_units >= 0),
    remaining_units NUMERIC(20, 8) NOT NULL CHECK (remaining_units >= 0),
    fee_jpy NUMERIC(20, 8) NOT NULL DEFAULT 0 CHECK (fee_jpy >= 0),
    is_fee_free BOOLEAN NOT NULL DEFAULT FALSE,
    placed_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ,
    cancelled_at TIMESTAMPTZ,
    last_status_checked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (filled_units <= ordered_units),
    CHECK (remaining_units = ordered_units - filled_units)
);

CREATE INDEX idx_orders_asset_code_status_placed_at
    ON orders (asset_code, status, placed_at DESC);

CREATE INDEX idx_orders_side_status_placed_at
    ON orders (side, status, placed_at DESC);

CREATE TABLE order_events (
    id BIGSERIAL PRIMARY KEY,
    order_id BIGINT NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    job_run_id BIGINT REFERENCES job_runs (id) ON DELETE SET NULL,
    event_type TEXT NOT NULL CHECK (
        event_type IN (
            'submitted',
            'opened',
            'partial_fill',
            'filled',
            'cancel_requested',
            'cancelled',
            'expired',
            'rejected',
            'sync_failed'
        )
    ),
    from_status TEXT,
    to_status TEXT,
    event_at TIMESTAMPTZ NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_order_events_order_id_event_at
    ON order_events (order_id, event_at ASC);

CREATE TABLE trade_executions (
    id BIGSERIAL PRIMARY KEY,
    order_id BIGINT NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    exchange_execution_id TEXT NOT NULL UNIQUE,
    executed_at TIMESTAMPTZ NOT NULL,
    price_jpy NUMERIC(20, 8) NOT NULL CHECK (price_jpy > 0),
    executed_units NUMERIC(20, 8) NOT NULL CHECK (executed_units > 0),
    fee_jpy NUMERIC(20, 8) NOT NULL DEFAULT 0 CHECK (fee_jpy >= 0),
    is_partial_fill BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_trade_executions_order_id_executed_at
    ON trade_executions (order_id, executed_at ASC);
