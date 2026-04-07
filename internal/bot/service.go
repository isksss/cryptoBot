package bot

import (
	"context"
	"log/slog"
)

type priceSyncer interface {
	SyncPriceAndBalances(ctx context.Context, requestedBy string, reason string) (int64, error)
}

type orderSyncer interface {
	ReconcileOrders(ctx context.Context, requestedBy string, reason string) (int64, error)
}

// Service は API と同居する bot 常駐処理の入口です。
type Service struct {
	logger       *slog.Logger
	priceSyncer  priceSyncer
	orderSyncer  orderSyncer
}

// NewService は起動時に実行する同期処理群を束ねます。
func NewService(logger *slog.Logger, priceSyncer priceSyncer, orderSyncer orderSyncer) *Service {
	return &Service{
		logger:      logger,
		priceSyncer: priceSyncer,
		orderSyncer: orderSyncer,
	}
}

// Run はプロセス寿命を管理し、起動直後の価格同期と注文同期を行います。
func (s *Service) Run(ctx context.Context) error {
	s.logger.Info("bot service started")

	if s.priceSyncer != nil {
		jobRunID, err := s.priceSyncer.SyncPriceAndBalances(ctx, "startup", "プロセス起動時の初期スナップショット")
		if err != nil {
			s.logger.Error("initial snapshot failed", slog.Any("error", err))
		} else {
			s.logger.Info("initial snapshot completed", slog.Int64("jobRunId", jobRunID))
		}
	}

	if s.orderSyncer != nil {
		jobRunID, err := s.orderSyncer.ReconcileOrders(ctx, "startup", "プロセス起動時の注文同期")
		if err != nil {
			s.logger.Error("initial order reconcile failed", slog.Any("error", err))
		} else {
			s.logger.Info("initial order reconcile completed", slog.Int64("jobRunId", jobRunID))
		}
	}

	<-ctx.Done()
	s.logger.Info("bot service stopped")
	return nil
}
