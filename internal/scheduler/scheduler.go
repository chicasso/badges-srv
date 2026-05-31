package scheduler

import (
	"context"
	"time"

	"github.com/robfig/cron/v3"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/ohhcgan/badges-srv/internal/mailer"
	"github.com/ohhcgan/badges-srv/internal/poster"
	controllers "github.com/ohhcgan/badges-srv/internal/server/controllers"
	"github.com/ohhcgan/badges-srv/internal/stats"
	"github.com/ohhcgan/badges-srv/internal/user"
)

const (
	workerPoolSize = 10 /* To avoid reaching github's api rate limit */
)

const (
	cronString = "0 2 1 * *" /* "0 2 1 * *" -> minute = 0, hour = 2, day = 1 -> 02:00 UTC on the 1st of each month. */
)

type Scheduler struct {
	cron       *cron.Cron
	collector  *stats.Collector
	generator  *poster.Generator
	mailer     *mailer.Mailer
	userStore  *user.Store
	statsStore *stats.Store
	logger     *zap.Logger
}

func New(
	collector *stats.Collector,
	generator *poster.Generator,
	mailer *mailer.Mailer,
	userStore *user.Store,
	statsStore *stats.Store,
	logger *zap.Logger,
) *Scheduler {
	s := &Scheduler{
		cron:       cron.New(cron.WithLocation(time.UTC)),
		collector:  collector,
		generator:  generator,
		mailer:     mailer,
		userStore:  userStore,
		statsStore: statsStore,
		logger:     logger,
	}

	s.cron.AddFunc(cronString, s.RunMonthlyJob)
	return s
}

func (s *Scheduler) Start() {
	s.cron.Start()
	s.logger.Info("scheduler started — monthly job fires at 02:00 UTC on the 1st of each month")
}

func (s *Scheduler) Stop() {
	ctx := s.cron.Stop() /* Stops and waits for every pending job to finish */
	<-ctx.Done()
}

/* It can also be triggered on demand using Admin API. */
func (s *Scheduler) RunMonthlyJob() {
	prevMonth, _ := controllers.GetTargetMonth("", "")

	s.logger.Info("monthly job started", zap.String("processing_month", prevMonth.Format("2006-01")))

	ctx := context.Background()
	/**
	 * TODO: should use Batching & Cursor pagination
	 */
	users, err := s.userStore.ListAll(ctx)
	if err != nil {
		s.logger.Error("failed to list users for monthly job", zap.Error(err))
		return
	}

	s.logger.Info("processing users", zap.Int("count", len(users)))

	/* Bounded worker pool to avoid hitting ratelimit of GitHub API. */
	/* TODO: we are not making use of errgroup, use it */

	grp, ctx := errgroup.WithContext(ctx)
	sem := make(chan struct{}, workerPoolSize)

	for _, u := range users {
		usr := u

		grp.Go(func() error {
			sem <- struct{}{}

			defer func() {
				<-sem
			}()

			s.processUser(ctx, usr, prevMonth)
			return nil
		})
	}

	_ = grp.Wait()

	s.logger.Info("monthly job finished", zap.String("month", prevMonth.Format("2006-01")))
}

func (s *Scheduler) processUser(ctx context.Context, u *user.User, month time.Time) {
	log := s.logger.With(zap.String("user", u.GithubLogin), zap.String("month", month.Format("2006-01")))

	userSt, err := s.collector.CollectForUser(ctx, u, month)
	if err != nil {
		log.Error("stats collection failed", zap.Error(err))
		_ = s.statsStore.LogEmail(ctx, u.ID, month, "failed", "stats collection: "+err.Error())
		return
	}

	posterPNG, err := s.generator.Generate(u, userSt)
	if err != nil {
		log.Error("poster generation failed", zap.Error(err))
		_ = s.statsStore.LogEmail(ctx, u.ID, month, "failed", "poster generation: "+err.Error())
		return
	}

	if err := s.mailer.SendPoster(u, userSt, posterPNG); err != nil {
		log.Error("email delivery failed", zap.Error(err))
		_ = s.statsStore.LogEmail(ctx, u.ID, month, "failed", "email: "+err.Error())
		return
	}

	_ = s.statsStore.LogEmail(ctx, u.ID, month, "sent", "")

	log.Info("activity report sent successfully")
}
