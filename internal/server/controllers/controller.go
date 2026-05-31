package server

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/ohhcgan/badges-srv/internal/auth"
	"github.com/ohhcgan/badges-srv/internal/stats"
	"github.com/ohhcgan/badges-srv/internal/user"
	"github.com/ohhcgan/badges-srv/pkg/dto"
)

type Controllers struct {
	logger       *zap.Logger
	userStore    *user.Store
	statsStore   *stats.Store
	monthlyJobFn func()
}

func NewControllers(
	userStore *user.Store,
	statsStore *stats.Store,
	logger *zap.Logger,
	monthlyJobFn func(),
) *Controllers {
	return &Controllers{
		logger:       logger,
		userStore:    userStore,
		statsStore:   statsStore,
		monthlyJobFn: monthlyJobFn,
	}
}

/**
 * Controllers
 */
func (h *Controllers) HealthCheck(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, dto.Response[any]{
		Message: "ok",
		Success: true,
		Data:    nil,
		Error:   nil,
	})
}

func (h *Controllers) GetMe(ctx *gin.Context) {
	userID, _ := auth.UserIDFromContext(ctx.Request.Context())

	u, err := h.userStore.FindByID(ctx.Request.Context(), userID)
	if errors.Is(err, user.ErrNotFound) {
		ctx.JSON(http.StatusNotFound, dto.Response[any]{
			Success: false,
			Data:    nil,
			Message: "user not found",
			Error: &dto.ErrorResponse{
				Message: "user not found",
				Code:    dto.NOT_FOUND,
			},
		})
		return
	}

	if err != nil {
		h.logger.Error("get user info query failed", zap.Error(err))
		ctx.JSON(http.StatusInternalServerError, dto.Response[any]{
			Success: false,
			Data:    nil,
			Message: "something unexpected happened",
			Error: &dto.ErrorResponse{
				Message: "something unexpected happened",
				Code:    dto.INTERNAL_SERVER_ERROR,
			},
		})
		return
	}

	ctx.JSON(http.StatusOK, dto.Response[map[string]any]{
		Success: true,
		Data: &map[string]any{
			"id":         u.ID,
			"github_id":  u.GithubID,
			"login":      u.GithubLogin,
			"name":       u.Name,
			"email":      u.Email,
			"avatar_url": u.AvatarURL,
			"created_at": u.CreatedAt,
		},
		Message: "user fetched",
		Error:   nil,
	})
}

func (h *Controllers) GetStats(ctx *gin.Context) {
	userID, _ := auth.UserIDFromContext(ctx.Request.Context())

	year := ctx.Request.URL.Query().Get("year")
	month := ctx.Request.URL.Query().Get("month")

	targetMonth, err := GetTargetMonth(year, month)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, dto.Response[any]{
			Success: false,
			Message: "invalid year or month",
			Data:    nil,
			Error: &dto.ErrorResponse{
				Message: "invalid year or month",
				Code:    dto.BAD_REQUEST,
			},
		})
		return
	}

	st, err := h.statsStore.FindByUserAndMonth(ctx.Request.Context(), userID, targetMonth)
	if errors.Is(err, stats.ErrNotFound) {
		ctx.JSON(http.StatusNotFound, dto.Response[any]{
			Success: true,
			Message: "no stats for this month yet",
			Data:    nil,
			Error:   nil,
		})
		return
	}

	if err != nil {
		h.logger.Error("get stats query failed", zap.Error(err))
		ctx.JSON(http.StatusInternalServerError, dto.Response[any]{
			Success: false,
			Message: "something unexpected happened",
			Data:    nil,
			Error: &dto.ErrorResponse{
				Message: "something unexpected happened",
				Code:    dto.INTERNAL_SERVER_ERROR,
			},
		})
		return
	}

	resp := statsResponse(st)
	ctx.JSON(http.StatusOK, dto.Response[map[string]any]{
		Success: true,
		Message: "fetched",
		Data:    &resp,
		Error:   nil,
	})
}

func (h *Controllers) GetStatsHistory(ctx *gin.Context) {
	userID, _ := auth.UserIDFromContext(ctx.Request.Context())

	monthsNum := 12
	list, err := h.statsStore.ListForUser(ctx.Request.Context(), userID, monthsNum)
	if err != nil {
		h.logger.Error("get stats history query failed", zap.Error(err))
		ctx.JSON(http.StatusInternalServerError, dto.Response[any]{
			Success: false,
			Message: "something unexpected happened",
			Data:    nil,
			Error: &dto.ErrorResponse{
				Message: "something unexpected happened",
				Code:    dto.INTERNAL_SERVER_ERROR,
			},
		})
		return
	}

	result := make([]map[string]any, 0, len(list))
	for _, st := range list {
		result = append(result, statsResponse(st))
	}
	ctx.JSON(http.StatusOK, dto.Response[map[string]any]{
		Success: true,
		Message: "history fetched",
		Data:    &map[string]any{"history": result},
		Error:   nil,
	})
}

func (h *Controllers) TriggerMonthly(ctx *gin.Context) {
	if h.monthlyJobFn == nil {
		ctx.JSON(http.StatusServiceUnavailable, dto.Response[any]{
			Message: "job not available",
			Success: false,
			Data:    nil,
			Error: &dto.ErrorResponse{
				Message: "job not available",
				Code:    dto.SERVICE_TEMP_UNAVAILABLE,
			},
		})
		return
	}

	go h.monthlyJobFn()

	ctx.JSON(http.StatusAccepted, dto.Response[any]{
		Message: "job started",
		Success: true,
		Data:    nil,
		Error:   nil,
	})
}

/**
 * Helpers
 */
func statsResponse(st *stats.MonthlyStats) map[string]any {
	resp := map[string]any{
		"id":                        st.ID,
		"user_id":                   st.UserID,
		"month":                     st.StatMonth.Format("2006-01"),
		"total_commits":             st.TotalCommits,
		"repos_created":             st.ReposCreated,
		"open_source_contributions": st.OpenSourceContributions,
		"created_at":                st.CreatedAt,
	}
	if st.CommitPctChange.Valid {
		resp["commit_pct_change"] = st.CommitPctChange.Float64
	} else {
		resp["commit_pct_change"] = nil
	}
	return resp
}

func GetTargetMonth(year, month string) (time.Time, error) {
	var targetMonth time.Time

	if year != "" && month != "" {
		year, yearParseErr := strconv.Atoi(year)
		month, monthParseErr := strconv.Atoi(month)

		if month < 1 || month > 12 ||
			yearParseErr != nil || monthParseErr != nil ||
			(year >= int(time.Now().Year()) && month >= int(time.Now().Month())) {
			return time.Time{}, errors.New("invalid year or month")
		}
		targetMonth = time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	} else {
		/* Fall back to prev month */

		now := time.Now().UTC()
		monthOffset := time.Month(1)
		yearOffset := 0

		if now.Month()-monthOffset <= 0 {
			yearOffset += 1
			monthOffset = -11
		}

		targetMonth = time.Date(now.Year()-yearOffset, now.Month()-monthOffset, 1, 0, 0, 0, 0, time.UTC)
	}
	return targetMonth, nil
}

func (h *Controllers) NoRoute(ctx *gin.Context) {
	ctx.JSON(http.StatusNotFound, dto.Response[any]{
		Message: "404 not found",
		Success: false,
		Data:    nil,
		Error: &dto.ErrorResponse{
			Message: "404 not found",
			Code:    dto.NOT_FOUND,
		},
	})
}
