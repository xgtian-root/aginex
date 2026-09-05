package app

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xgtian-root/aginex/backend/framework/httpx"
	"github.com/xgtian-root/aginex/backend/framework/module"
	"github.com/xgtian-root/aginex/backend/framework/ratelimit"
	"github.com/xgtian-root/aginex/backend/internal/auth"
)

func (a *App) operationRateLimiter(
	policy module.RateLimitPolicy,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		var key string
		switch policy.Subject {
		case module.RateLimitByIP:
			key = c.ClientIP()
		case module.RateLimitByActor:
			actor, ok := module.AuthenticatedActorFromContext(
				c.Request.Context(),
			)
			if !ok {
				httpx.AbortProblem(
					c,
					http.StatusInternalServerError,
					"RATE_LIMIT_ACTOR_UNAVAILABLE",
					"Rate-limit actor is unavailable",
					"The authenticated actor was not attached to the request.",
				)
				return
			}
			key = string(actor.Kind) + ":" + actor.ID
		default:
			httpx.AbortProblem(
				c,
				http.StatusInternalServerError,
				"RATE_LIMIT_POLICY_INVALID",
				"Request protection is unavailable",
				"The operation rate-limit policy is invalid.",
			)
			return
		}
		if !a.consumeRateLimit(
			c,
			policy.Namespace,
			policy.Limit,
			policy.Window,
			key,
		) {
			return
		}
		c.Next()
	}
}

func (a *App) consumeRateLimit(
	c *gin.Context,
	namespace string,
	limit uint64,
	window time.Duration,
	key string,
) bool {
	decision, err := a.limiter.Consume(c.Request.Context(), ratelimit.Request{
		Namespace: namespace,
		Key:       key,
		Limit:     limit,
		Cost:      1,
		Window:    window,
	})
	if err != nil {
		httpx.AbortProblem(
			c,
			http.StatusServiceUnavailable,
			"RATE_LIMIT_UNAVAILABLE",
			"Request protection is unavailable",
			"The request cannot be processed safely right now.",
		)
		return false
	}
	c.Header("RateLimit-Limit", strconv.FormatUint(decision.Limit, 10))
	c.Header("RateLimit-Remaining", strconv.FormatUint(decision.Remaining, 10))
	c.Header("RateLimit-Reset", strconv.FormatInt(decision.ResetAt.Unix(), 10))
	if decision.Allowed {
		return true
	}
	retrySeconds := int64((decision.RetryAfter + time.Second - 1) / time.Second)
	if retrySeconds < 1 {
		retrySeconds = 1
	}
	c.Header("Retry-After", strconv.FormatInt(retrySeconds, 10))
	httpx.AbortProblem(
		c,
		http.StatusTooManyRequests,
		"RATE_LIMITED",
		"Too many requests",
		"Wait before trying this operation again.",
	)
	return false
}

func (a *App) enforceLoginAccountRateLimit(c *gin.Context, email string) bool {
	return a.consumeRateLimit(
		c,
		"auth.login.account",
		a.cfg.RateLimit.LoginLimit,
		a.cfg.RateLimit.LoginWindow,
		auth.NormalizePasswordSubject(email),
	)
}
