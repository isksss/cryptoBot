-- name: ListLatestPrices :many
WITH ranked AS (
    SELECT DISTINCT ON (asset_code)
        id,
        asset_code,
        price_jpy::text AS price_jpy,
        captured_at,
        source
    FROM price_snapshots
    WHERE (sqlc.narg('asset_code')::text IS NULL OR asset_code = sqlc.narg('asset_code')::text)
    ORDER BY asset_code, captured_at DESC
)
SELECT id, asset_code, price_jpy, captured_at, source
FROM ranked
ORDER BY asset_code;

-- name: ListPriceHistory :many
SELECT
    id,
    asset_code,
    price_jpy::text AS price_jpy,
    captured_at,
    source
FROM price_snapshots
WHERE asset_code = sqlc.arg('asset_code')
  AND (sqlc.narg('from_at')::timestamptz IS NULL OR captured_at >= sqlc.narg('from_at')::timestamptz)
  AND (sqlc.narg('to_at')::timestamptz IS NULL OR captured_at <= sqlc.narg('to_at')::timestamptz)
ORDER BY captured_at DESC
LIMIT sqlc.arg('limit_count');

-- name: ListOrders :many
SELECT
    id,
    exchange_order_id,
    client_order_id,
    asset_code,
    side,
    order_type,
    status,
    price_jpy::text AS price_jpy,
    ordered_units::text AS ordered_units,
    filled_units::text AS filled_units,
    remaining_units::text AS remaining_units,
    fee_jpy::text AS fee_jpy,
    is_fee_free,
    placed_at,
    expires_at,
    cancelled_at,
    last_status_checked_at
FROM orders
WHERE (sqlc.narg('asset_code')::text IS NULL OR asset_code = sqlc.narg('asset_code')::text)
  AND (sqlc.narg('side')::text IS NULL OR side = sqlc.narg('side')::text)
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
ORDER BY placed_at DESC, id DESC
LIMIT sqlc.arg('limit_count');

-- name: GetOrder :one
SELECT
    id,
    exchange_order_id,
    client_order_id,
    asset_code,
    side,
    order_type,
    status,
    price_jpy::text AS price_jpy,
    ordered_units::text AS ordered_units,
    filled_units::text AS filled_units,
    remaining_units::text AS remaining_units,
    fee_jpy::text AS fee_jpy,
    is_fee_free,
    placed_at,
    expires_at,
    cancelled_at,
    last_status_checked_at
FROM orders
WHERE id = sqlc.arg('id');

-- name: ListOrderEventsByOrderID :many
SELECT
    id,
    event_type,
    from_status,
    to_status,
    event_at,
    payload
FROM order_events
WHERE order_id = sqlc.arg('order_id')
ORDER BY event_at ASC, id ASC;

-- name: ListExecutions :many
SELECT
    te.id,
    te.order_id,
    te.exchange_execution_id,
    te.executed_at,
    te.price_jpy::text AS price_jpy,
    te.executed_units::text AS executed_units,
    te.fee_jpy::text AS fee_jpy,
    te.is_partial_fill
FROM trade_executions te
JOIN orders o ON o.id = te.order_id
WHERE (sqlc.narg('asset_code')::text IS NULL OR o.asset_code = sqlc.narg('asset_code')::text)
  AND (sqlc.narg('order_id')::bigint IS NULL OR te.order_id = sqlc.narg('order_id')::bigint)
ORDER BY te.executed_at DESC, te.id DESC
LIMIT sqlc.arg('limit_count');

-- name: ListJobRuns :many
SELECT
    id,
    job_type,
    status,
    scheduled_for,
    started_at,
    finished_at,
    error_code,
    error_message
FROM job_runs
WHERE (sqlc.narg('job_type')::text IS NULL OR job_type = sqlc.narg('job_type')::text)
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
ORDER BY started_at DESC, id DESC
LIMIT sqlc.arg('limit_count');

-- name: ListLatestJobRuns :many
SELECT
    id,
    job_type,
    status,
    scheduled_for,
    started_at,
    finished_at,
    error_code,
    error_message
FROM job_runs
ORDER BY started_at DESC, id DESC
LIMIT sqlc.arg('limit_count');

-- name: ListLatestBalances :many
WITH ranked AS (
    SELECT DISTINCT ON (asset_code)
        asset_code,
        available_amount::text AS available_amount,
        locked_amount::text AS locked_amount,
        captured_at
    FROM balance_snapshots
    ORDER BY asset_code, captured_at DESC
)
SELECT asset_code, available_amount, locked_amount, captured_at
FROM ranked
ORDER BY asset_code;

-- name: CountOpenOrders :one
SELECT COUNT(*)::bigint
FROM orders
WHERE status IN ('open', 'partially_filled', 'cancel_requested');

-- name: CountUnresolvedPreviousDayOrders :one
SELECT COUNT(*)::bigint
FROM orders
WHERE status IN ('open', 'partially_filled', 'cancel_requested')
  AND (placed_at AT TIME ZONE 'Asia/Tokyo')::date < (NOW() AT TIME ZONE 'Asia/Tokyo')::date;

-- name: ListWeeklyConsumedBuyUnits :many
SELECT
    asset_code,
    COALESCE(SUM(ordered_units), 0)::text AS consumed_units
FROM orders
WHERE side = 'buy'
  AND status IN ('open', 'partially_filled', 'filled', 'cancelled', 'expired')
  AND placed_at >= sqlc.arg('window_started_at')
GROUP BY asset_code
ORDER BY asset_code;

-- name: InsertJobRun :one
INSERT INTO job_runs (
    job_type,
    status,
    scheduled_for,
    started_at,
    metadata
) VALUES (
    sqlc.arg('job_type'),
    sqlc.arg('status'),
    sqlc.arg('scheduled_for'),
    sqlc.arg('started_at'),
    sqlc.arg('metadata')
)
RETURNING id, job_type, status, scheduled_for, started_at, finished_at, error_code, error_message;

-- name: MarkJobRunSucceeded :exec
UPDATE job_runs
SET status = 'succeeded',
    finished_at = sqlc.arg('finished_at'),
    error_code = NULL,
    error_message = NULL
WHERE id = sqlc.arg('id');

-- name: MarkJobRunFailed :exec
UPDATE job_runs
SET status = 'failed',
    finished_at = sqlc.arg('finished_at'),
    error_code = sqlc.arg('error_code'),
    error_message = sqlc.arg('error_message')
WHERE id = sqlc.arg('id');

-- name: InsertPriceSnapshot :one
INSERT INTO price_snapshots (
    asset_code,
    price_jpy,
    captured_at,
    source
) VALUES (
    sqlc.arg('asset_code'),
    sqlc.arg('price_jpy'),
    sqlc.arg('captured_at'),
    sqlc.arg('source')
)
RETURNING id, asset_code, price_jpy::text AS price_jpy, captured_at, source;

-- name: InsertBalanceSnapshot :one
INSERT INTO balance_snapshots (
    job_run_id,
    asset_code,
    available_amount,
    locked_amount,
    captured_at
) VALUES (
    sqlc.narg('job_run_id'),
    sqlc.arg('asset_code'),
    sqlc.arg('available_amount'),
    sqlc.arg('locked_amount'),
    sqlc.arg('captured_at')
)
RETURNING id, job_run_id, asset_code, available_amount::text AS available_amount, locked_amount::text AS locked_amount, captured_at;
