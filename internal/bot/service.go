package bot

import (
	"context"
	"log/slog"
)

type priceSyncer interface {
	SyncPriceAndBalances(ctx context.Context, requestedBy string, reason string) (int64, error)
}

// Service is the process-local bot runtime. Scheduling and actual trade logic
// will live here; for now it provides the long-lived process boundary shared
// with the management API.
type Service struct {
	logger      *slog.Logger
	priceSyncer priceSyncer
}

func NewService(logger *slog.Logger, priceSyncer priceSyncer) *Service {
	return &Service{
		logger:      logger,
		priceSyncer: priceSyncer,
	}
}

func (s *Service) Run(ctx context.Context) error {
	s.logger.Info("bot service started")

	if s.priceSyncer != nil {
		jobRunID, err := s.priceSyncer.SyncPriceAndBalances(ctx, "startup", "initial snapshot on process start")
		if err != nil {
			s.logger.Error("initial snapshot failed", slog.Any("error", err))
		} else {
			s.logger.Info("initial snapshot completed", slog.Int64("jobRunId", jobRunID))
		}
	}

	<-ctx.Done()
	s.logger.Info("bot service stopped")
	return nil
}
