package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/isksss/cryptoBot/internal/gmo"
	"github.com/isksss/cryptoBot/internal/store"
)

type queryStore interface {
	InsertJobRun(ctx context.Context, arg store.InsertJobRunParams) (store.InsertJobRunRow, error)
	MarkJobRunFailed(ctx context.Context, arg store.MarkJobRunFailedParams) error
	MarkJobRunSucceeded(ctx context.Context, arg store.MarkJobRunSucceededParams) error
	InsertBalanceSnapshot(ctx context.Context, arg store.InsertBalanceSnapshotParams) (store.InsertBalanceSnapshotRow, error)
	InsertPriceSnapshot(ctx context.Context, arg store.InsertPriceSnapshotParams) (store.InsertPriceSnapshotRow, error)
}

type gmoClient interface {
	GetAssets(ctx context.Context) ([]gmo.Asset, error)
	GetTicker(ctx context.Context, symbol string) (gmo.Ticker, error)
}

type Service struct {
	logger  *slog.Logger
	queries queryStore
	client  gmoClient
	now     func() time.Time
}

func NewService(logger *slog.Logger, queries queryStore, client gmoClient) *Service {
	return &Service{
		logger:  logger,
		queries: queries,
		client:  client,
		now:     time.Now,
	}
}

func (s *Service) SyncPriceAndBalances(ctx context.Context, requestedBy string, reason string) (int64, error) {
	now := s.now().UTC()
	metadata, err := json.Marshal(map[string]string{
		"requestedBy": requestedBy,
		"reason":      reason,
	})
	if err != nil {
		return 0, err
	}

	jobRun, err := s.queries.InsertJobRun(ctx, store.InsertJobRunParams{
		JobType:      "price_fetch",
		Status:       "running",
		ScheduledFor: toPgTimestamptz(now),
		StartedAt:    toPgTimestamptz(now),
		Metadata:     metadata,
	})
	if err != nil {
		return 0, err
	}

	if err := s.syncPriceAndBalances(ctx, jobRun.ID); err != nil {
		markErr := s.queries.MarkJobRunFailed(ctx, store.MarkJobRunFailedParams{
			ID:           jobRun.ID,
			FinishedAt:   toPgTimestamptz(s.now().UTC()),
			ErrorCode:    stringPtr("sync_failed"),
			ErrorMessage: stringPtr(err.Error()),
		})
		if markErr != nil {
			s.logger.Error("mark job failed", slog.Any("error", markErr), slog.Int64("jobRunId", jobRun.ID))
		}
		return jobRun.ID, err
	}

	if err := s.queries.MarkJobRunSucceeded(ctx, store.MarkJobRunSucceededParams{
		ID:         jobRun.ID,
		FinishedAt: toPgTimestamptz(s.now().UTC()),
	}); err != nil {
		return jobRun.ID, err
	}

	return jobRun.ID, nil
}

func (s *Service) syncPriceAndBalances(ctx context.Context, jobRunID int64) error {
	assets, err := s.client.GetAssets(ctx)
	if err != nil {
		return fmt.Errorf("get assets: %w", err)
	}

	for _, asset := range assets {
		if asset.Symbol != "JPY" && asset.Symbol != "BTC" && asset.Symbol != "ETH" {
			continue
		}

		amount, err := parseNumeric(asset.Amount)
		if err != nil {
			return fmt.Errorf("parse amount for %s: %w", asset.Symbol, err)
		}
		available, err := parseNumeric(asset.Available)
		if err != nil {
			return fmt.Errorf("parse available for %s: %w", asset.Symbol, err)
		}

		locked, err := subtractNumeric(amount, available)
		if err != nil {
			return fmt.Errorf("calculate locked amount for %s: %w", asset.Symbol, err)
		}

		if _, err := s.queries.InsertBalanceSnapshot(ctx, store.InsertBalanceSnapshotParams{
			JobRunID:        &jobRunID,
			AssetCode:       asset.Symbol,
			AvailableAmount: available,
			LockedAmount:    locked,
			CapturedAt:      toPgTimestamptz(s.now().UTC()),
		}); err != nil {
			return fmt.Errorf("insert balance snapshot for %s: %w", asset.Symbol, err)
		}
	}

	for _, symbol := range []string{"BTC", "ETH"} {
		ticker, err := s.client.GetTicker(ctx, symbol)
		if err != nil {
			return fmt.Errorf("get ticker for %s: %w", symbol, err)
		}

		price, err := parseNumeric(ticker.Last)
		if err != nil {
			return fmt.Errorf("parse ticker price for %s: %w", symbol, err)
		}

		if _, err := s.queries.InsertPriceSnapshot(ctx, store.InsertPriceSnapshotParams{
			AssetCode:  symbol,
			PriceJpy:   price,
			CapturedAt: toPgTimestamptz(ticker.Timestamp.UTC()),
			Source:     "gmo_public_ticker_last",
		}); err != nil {
			return fmt.Errorf("insert price snapshot for %s: %w", symbol, err)
		}
	}

	s.logger.Info("synced gmo balances and prices", slog.Int64("jobRunId", jobRunID))
	return nil
}

func parseNumeric(value string) (pgtype.Numeric, error) {
	rat, ok := new(big.Rat).SetString(value)
	if !ok {
		return pgtype.Numeric{}, fmt.Errorf("invalid decimal: %s", value)
	}

	var n pgtype.Numeric
	if err := n.ScanScientific(rat.FloatString(8)); err != nil {
		return pgtype.Numeric{}, err
	}
	return n, nil
}

func subtractNumeric(left pgtype.Numeric, right pgtype.Numeric) (pgtype.Numeric, error) {
	leftRat, err := numericToRat(left)
	if err != nil {
		return pgtype.Numeric{}, err
	}
	rightRat, err := numericToRat(right)
	if err != nil {
		return pgtype.Numeric{}, err
	}

	result := new(big.Rat).Sub(leftRat, rightRat)
	if result.Sign() < 0 {
		result = big.NewRat(0, 1)
	}

	var out pgtype.Numeric
	if err := out.ScanScientific(result.FloatString(8)); err != nil {
		return pgtype.Numeric{}, err
	}
	return out, nil
}

func numericToRat(n pgtype.Numeric) (*big.Rat, error) {
	if !n.Valid || n.Int == nil {
		return big.NewRat(0, 1), nil
	}

	r := new(big.Rat).SetInt(n.Int)
	if n.Exp < 0 {
		divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(-n.Exp)), nil)
		return r.Quo(r, new(big.Rat).SetInt(divisor)), nil
	}
	if n.Exp > 0 {
		multiplier := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n.Exp)), nil)
		return r.Mul(r, new(big.Rat).SetInt(multiplier)), nil
	}
	return r, nil
}

func toPgTimestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{
		Time:  t,
		Valid: true,
	}
}

func stringPtr(value string) *string {
	return &value
}
