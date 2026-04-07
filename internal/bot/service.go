package bot

import (
	"context"
	"log/slog"
	"time"
)

type priceSyncer interface {
	SyncPriceAndBalances(ctx context.Context, requestedBy string, reason string) (int64, error)
}

type orderSyncer interface {
	ReconcileOrders(ctx context.Context, requestedBy string, reason string) (int64, error)
}

type dailyTrader interface {
	DailyTrade(ctx context.Context, requestedBy string, reason string) (int64, error)
}

// Service は API と同居する bot 常駐処理と scheduler の入口です。
type Service struct {
	logger                 *slog.Logger
	priceSyncer            priceSyncer
	orderSyncer            orderSyncer
	dailyTrader            dailyTrader
	priceSyncInterval      time.Duration
	orderReconcileInterval time.Duration
	now                    func() time.Time
}

// NewService は起動時処理と定期実行設定を束ねた bot サービスを作ります。
func NewService(
	logger *slog.Logger,
	priceSyncer priceSyncer,
	orderSyncer orderSyncer,
	dailyTrader dailyTrader,
	priceSyncInterval time.Duration,
	orderReconcileInterval time.Duration,
) *Service {
	if priceSyncInterval <= 0 {
		priceSyncInterval = time.Hour
	}
	if orderReconcileInterval <= 0 {
		orderReconcileInterval = 5 * time.Minute
	}

	return &Service{
		logger:                 logger,
		priceSyncer:            priceSyncer,
		orderSyncer:            orderSyncer,
		dailyTrader:            dailyTrader,
		priceSyncInterval:      priceSyncInterval,
		orderReconcileInterval: orderReconcileInterval,
		now:                    time.Now,
	}
}

// Run は起動直後の初期同期と、価格同期・注文同期・日次売買の scheduler を起動します。
func (s *Service) Run(ctx context.Context) error {
	s.logger.Info("bot service started")

	s.runStartupTasks(ctx)

	if s.priceSyncer != nil {
		go s.runPeriodic(ctx, s.priceSyncInterval, "periodic price sync", func(runCtx context.Context) {
			if _, err := s.priceSyncer.SyncPriceAndBalances(runCtx, "scheduler", "定期価格同期"); err != nil {
				s.logger.Error("periodic price sync failed", slog.Any("error", err))
			}
		})
	}

	if s.orderSyncer != nil {
		go s.runPeriodic(ctx, s.orderReconcileInterval, "periodic order reconcile", func(runCtx context.Context) {
			if _, err := s.orderSyncer.ReconcileOrders(runCtx, "scheduler", "定期注文同期"); err != nil {
				s.logger.Error("periodic order reconcile failed", slog.Any("error", err))
			}
		})
	}

	if s.dailyTrader != nil {
		go s.runDaily(ctx, "daily trade", func(runCtx context.Context) {
			if _, err := s.dailyTrader.DailyTrade(runCtx, "scheduler", "日次売買スケジュール"); err != nil {
				s.logger.Error("daily trade failed", slog.Any("error", err))
			}
		})
	}

	<-ctx.Done()
	s.logger.Info("bot service stopped")
	return nil
}

// runStartupTasks はプロセス起動直後に 1 回だけ必要な同期を実行します。
func (s *Service) runStartupTasks(ctx context.Context) {
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
}

// runPeriodic は固定間隔のジョブを context 終了まで繰り返し実行します。
func (s *Service) runPeriodic(ctx context.Context, interval time.Duration, name string, fn func(context.Context)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.logger.Info(name)
			fn(ctx)
		}
	}
}

// runDaily は JST 0:00 に合わせて 1 日 1 回ジョブを実行します。
func (s *Service) runDaily(ctx context.Context, name string, fn func(context.Context)) {
	for {
		wait := time.Until(nextJSTMidnight(s.now()))
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			s.logger.Info(name)
			fn(ctx)
		}
	}
}

// nextJSTMidnight は次に到来する JST 0:00 の時刻を返します。
func nextJSTMidnight(now time.Time) time.Time {
	jst := time.FixedZone("Asia/Tokyo", 9*60*60)
	inJST := now.In(jst)
	next := time.Date(inJST.Year(), inJST.Month(), inJST.Day()+1, 0, 0, 0, 0, jst)
	return next
}
