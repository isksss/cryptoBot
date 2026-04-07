package bot

import (
	"context"
	"log/slog"
)

// Service is the process-local bot runtime. Scheduling and actual trade logic
// will live here; for now it provides the long-lived process boundary shared
// with the management API.
type Service struct {
	logger *slog.Logger
}

func NewService(logger *slog.Logger) *Service {
	return &Service{logger: logger}
}

func (s *Service) Run(ctx context.Context) error {
	s.logger.Info("bot service started")
	<-ctx.Done()
	s.logger.Info("bot service stopped")
	return nil
}
