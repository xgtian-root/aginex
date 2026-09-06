package tokenauth

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/xgtian-root/aginex/server/framework/authz"
	"github.com/xgtian-root/aginex/server/framework/httpx"
	"github.com/xgtian-root/aginex/server/framework/module"
)

const maxAuthorizationHeaderBytes = maxJWTBytes + len("Bearer ")

type authenticatedSubjectContextKey struct{}

// ContextWithAuthenticatedSubject attaches verified token subject and claims
// without retaining the raw bearer token.
func ContextWithAuthenticatedSubject(
	ctx context.Context,
	authenticated AuthenticatedSubject,
) (context.Context, error) {
	if ctx == nil || !validAuthenticatedSubject(authenticated) {
		return nil, fmt.Errorf(
			"%w: invalid authenticated subject",
			ErrInvalidState,
		)
	}
	return context.WithValue(
		ctx,
		authenticatedSubjectContextKey{},
		authenticated,
	), nil
}

// AuthenticatedSubjectFromContext returns verified access-token metadata. The
// raw bearer token is never placed in request context.
func AuthenticatedSubjectFromContext(
	ctx context.Context,
) (AuthenticatedSubject, bool) {
	if ctx == nil {
		return AuthenticatedSubject{}, false
	}
	authenticated, ok := ctx.Value(
		authenticatedSubjectContextKey{},
	).(AuthenticatedSubject)
	if !ok || !validAuthenticatedSubject(authenticated) {
		return AuthenticatedSubject{}, false
	}
	return authenticated, true
}

// BearerActorResolver loads application grants for one validated token
// subject. The returned actor must be a user actor with the same ID.
type BearerActorResolver interface {
	ResolveBearerActor(
		context.Context,
		AuthenticatedSubject,
	) (authz.Actor, error)
}

// BearerActorResolverFunc adapts a function to BearerActorResolver.
type BearerActorResolverFunc func(
	context.Context,
	AuthenticatedSubject,
) (authz.Actor, error)

func (function BearerActorResolverFunc) ResolveBearerActor(
	ctx context.Context,
	authenticated AuthenticatedSubject,
) (authz.Actor, error) {
	return function(ctx, authenticated)
}

// BearerMiddlewareConfig defines the reusable validation boundary. Login,
// refresh, revocation routes, rate limits, and audit events remain
// application-module responsibilities.
type BearerMiddlewareConfig struct {
	Service *Service
	Actors  BearerActorResolver
}

// NewBearerMiddleware validates one strict Authorization header, re-checks the
// active subject, loads its current grants, and attaches both token metadata
// and the module authenticated actor to the request context.
func NewBearerMiddleware(
	config BearerMiddlewareConfig,
) (gin.HandlerFunc, error) {
	if config.Service == nil {
		return nil, fmt.Errorf(
			"%w: token service is required",
			ErrInvalidConfig,
		)
	}
	if err := config.Service.validateCall(context.Background()); err != nil {
		return nil, fmt.Errorf(
			"%w: token service is not initialized",
			ErrInvalidConfig,
		)
	}
	if nilInterface(config.Actors) {
		return nil, fmt.Errorf(
			"%w: bearer actor resolver is required",
			ErrInvalidConfig,
		)
	}

	return func(c *gin.Context) {
		values := c.Request.Header.Values("Authorization")
		if len(values) != 1 {
			failBearer(c)
			return
		}
		rawAccess, err := AccessTokenFromAuthorization(values[0])
		if err != nil {
			failBearer(c)
			return
		}
		authenticated, err := config.Service.ValidateAccess(
			c.Request.Context(),
			rawAccess,
		)
		if err != nil {
			failBearer(c)
			return
		}
		actor, err := config.Actors.ResolveBearerActor(
			c.Request.Context(),
			authenticated,
		)
		if err != nil ||
			actor.Kind != authz.ActorKindUser ||
			actor.ID != authenticated.Subject.ID {
			failBearer(c)
			return
		}

		requestContext, err := ContextWithAuthenticatedSubject(
			c.Request.Context(),
			authenticated,
		)
		if err == nil {
			requestContext, err = module.ContextWithAuthenticatedActor(
				requestContext,
				actor,
			)
		}
		if err != nil {
			failBearer(c)
			return
		}
		c.Request = c.Request.WithContext(requestContext)
		c.Next()
	}, nil
}

// AccessTokenFromAuthorization parses exactly one canonical Bearer credential.
// It rejects whitespace normalization and combined/multiple credentials so
// proxies and applications cannot disagree about which token was validated.
func AccessTokenFromAuthorization(
	authorization string,
) (string, error) {
	if authorization == "" ||
		len(authorization) > maxAuthorizationHeaderBytes ||
		strings.TrimSpace(authorization) != authorization {
		return "", ErrInvalidAccess
	}
	separator := strings.IndexByte(authorization, ' ')
	if separator <= 0 ||
		!strings.EqualFold(
			authorization[:separator],
			bearerTokenType,
		) {
		return "", ErrInvalidAccess
	}
	rawAccess := authorization[separator+1:]
	if rawAccess == "" ||
		strings.ContainsAny(rawAccess, " \t\r\n,") {
		return "", ErrInvalidAccess
	}
	return rawAccess, nil
}

func failBearer(c *gin.Context) {
	c.Header("WWW-Authenticate", bearerTokenType)
	httpx.WriteProblem(
		c,
		http.StatusUnauthorized,
		"AUTHENTICATION_REQUIRED",
		"Authentication required",
		"A valid bearer access token is required.",
	)
	c.Abort()
}

func validAuthenticatedSubject(
	authenticated AuthenticatedSubject,
) bool {
	return authenticated.Subject.Active &&
		validID(authenticated.Subject.ID) &&
		authenticated.Subject.ID == authenticated.Claims.Subject &&
		validText(authenticated.Claims.Issuer, maxIssuerBytes) &&
		validText(authenticated.Claims.Audience, maxAudienceBytes) &&
		authenticated.Claims.Type == accessTokenType &&
		validID(authenticated.Claims.TokenID) &&
		validID(authenticated.Claims.FamilyID) &&
		validID(authenticated.Claims.DeviceID) &&
		authenticated.Claims.IssuedAt > 0 &&
		authenticated.Claims.ExpiresAt >
			authenticated.Claims.IssuedAt
}
