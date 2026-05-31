package stats

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
	"golang.org/x/oauth2"

	"github.com/ohhcgan/badges-srv/internal/crypto"
	ghClient "github.com/ohhcgan/badges-srv/internal/github"
	"github.com/ohhcgan/badges-srv/internal/user"
)

/**
 * Collector fetches GitHub stats for a user and stores them in the database.
 */
type Collector struct {
	userStore  *user.Store
	statsStore *Store
	cryptoKey  []byte
	logger     *zap.Logger
}

func NewCollector(
	userStore *user.Store,
	statsStore *Store,
	cryptoKey []byte,
	logger *zap.Logger,
) *Collector {
	return &Collector{
		userStore:  userStore,
		statsStore: statsStore,
		cryptoKey:  cryptoKey,
		logger:     logger,
	}
}

/**
 * TODO:
 * github AccessToken can expire, use RefreshToken for new AccessToken
 */
func (c *Collector) CollectForUser(ctx context.Context, u *user.User, month time.Time) (*MonthlyStats, error) {
	/* Decrypt the stored OAuth token. */
	tokenJSON, err := crypto.Decrypt(c.cryptoKey, u.EncryptedToken)
	if err != nil {
		return nil, fmt.Errorf("decrypting token for user %s: %w", u.GithubLogin, err)
	}

	var token oauth2.Token
	if err := json.Unmarshal(tokenJSON, &token); err != nil {
		return nil, fmt.Errorf("unmarshalling token for user %s: %w", u.GithubLogin, err)
	}

	ghClient := ghClient.NewClient(token.AccessToken, token.RefreshToken)

	/* From prev month start to prev month end */
	from := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 1, 0).Add(-time.Nanosecond)

	rawStats, err := ghClient.FetchMonthlyStats(ctx, u.GithubLogin, from, to)
	if err != nil {
		return nil, fmt.Errorf("fetching github stats for user %s: %w", u.GithubLogin, err)
	}

	prevMonth := from.AddDate(0, -1, 0)
	prevStats, err := c.statsStore.FindByUserAndMonth(ctx, u.ID, prevMonth)

	if err != nil && errors.Is(err, ErrNotFound) {
		prevStats = &MonthlyStats{}
		prevStats.TotalCommits = 0
		prevStats.ReposCreated = 0
		prevStats.OpenSourceContributions = 0
		prevStats.CommitPctChange = sql.NullFloat64{Float64: 0}
	} else if err != nil {
		return nil, fmt.Errorf("error: %w", err)
	}

	var pctChange sql.NullFloat64
	if prevStats.TotalCommits > 0 {
		delta := float64(rawStats.TotalCommits-prevStats.TotalCommits) / float64(prevStats.TotalCommits) * 100
		pctChange = sql.NullFloat64{Float64: delta, Valid: true}
	} else if prevStats.TotalCommits == 0 && rawStats.TotalCommits > 0 {
		/* First month for the user, treat as 100% incr */
		pctChange = sql.NullFloat64{Float64: 100, Valid: true}
	}

	/* TODO: bug, store total commits for current month only */
	st := &MonthlyStats{
		UserID:                  u.ID,
		StatMonth:               from,
		TotalCommits:            rawStats.TotalCommits,
		ReposCreated:            rawStats.ReposCreated,
		OpenSourceContributions: rawStats.OpenSourceContributions,
		CommitPctChange:         pctChange,
	}

	if err := c.statsStore.Upsert(ctx, st); err != nil {
		return nil, fmt.Errorf("upserting stats for user %s: %w", u.GithubLogin, err)
	}

	c.logger.Info("collected stats",
		zap.String("user", u.GithubLogin),
		zap.String("month", from.Format("2006-01")),
		zap.Int("commits", st.TotalCommits),
		zap.Int("repos", st.ReposCreated),
		zap.Int("oss", st.OpenSourceContributions),
	)
	return st, nil
}
