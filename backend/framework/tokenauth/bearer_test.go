package tokenauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xgtian-root/aginex/backend/framework/authz"
	"github.com/xgtian-root/aginex/backend/framework/httpx"
	"github.com/xgtian-root/aginex/backend/framework/module"
)

func TestBearerMiddlewareAttachesValidatedSubjectAndMatchingActor(
	t *testing.T,
) {
	service := newTestService(
		t,
		openTokenDatabase(t),
		newFakeClock(
			time.Date(2026, 7, 31, 13, 0, 0, 0, time.UTC),
		),
		newDeterministicReader(201),
	)
	pair, err := service.Issue(
		context.Background(),
		"user-1",
		Device{ID: "phone-1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	middleware, err := NewBearerMiddleware(
		BearerMiddlewareConfig{
			Service: service,
			Actors: BearerActorResolverFunc(func(
				_ context.Context,
				authenticated AuthenticatedSubject,
			) (authz.Actor, error) {
				return authz.NewUserActor(
					authenticated.Subject.ID,
					authz.Grant{
						Permission: "files:read",
						Scope:      authz.ScopeOwn,
					},
				), nil
			}),
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.GET("/protected", middleware, func(c *gin.Context) {
		authenticated, subjectOK := AuthenticatedSubjectFromContext(
			c.Request.Context(),
		)
		actor, actorOK := module.AuthenticatedActorFromContext(
			c.Request.Context(),
		)
		if !subjectOK || !actorOK {
			c.Status(http.StatusInternalServerError)
			return
		}
		if authenticated.Subject.ID != actor.ID ||
			authenticated.Claims.FamilyID != pair.FamilyID {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.Status(http.StatusNoContent)
	})

	request := httptest.NewRequest(
		http.MethodGet,
		"/protected",
		nil,
	)
	request.Header.Set(
		"Authorization",
		"Bearer "+pair.AccessToken.Value,
	)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf(
			"valid bearer status = %d body=%s",
			response.Code,
			response.Body.String(),
		)
	}
}

func TestBearerMiddlewareFailsClosedForMissingAmbiguousAndMismatchedActor(
	t *testing.T,
) {
	service := newTestService(
		t,
		openTokenDatabase(t),
		newFakeClock(
			time.Date(2026, 7, 31, 14, 0, 0, 0, time.UTC),
		),
		newDeterministicReader(31),
	)
	pair, err := service.Issue(
		context.Background(),
		"user-1",
		Device{ID: "phone-2"},
	)
	if err != nil {
		t.Fatal(err)
	}

	testCases := []struct {
		name    string
		headers []string
		actorID string
	}{
		{name: "missing", actorID: "user-1"},
		{
			name:    "multiple",
			headers: []string{"Bearer " + pair.AccessToken.Value, "Bearer other"},
			actorID: "user-1",
		},
		{
			name:    "malformed",
			headers: []string{"Bearer  " + pair.AccessToken.Value},
			actorID: "user-1",
		},
		{
			name:    "mismatched actor",
			headers: []string{"Bearer " + pair.AccessToken.Value},
			actorID: "user-2",
		},
		{
			name:    "wrong scheme",
			headers: []string{"not-bearer"},
			actorID: "user-1",
		},
	}
	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			handlerCalled := false
			middleware, err := NewBearerMiddleware(
				BearerMiddlewareConfig{
					Service: service,
					Actors: BearerActorResolverFunc(func(
						_ context.Context,
						_ AuthenticatedSubject,
					) (authz.Actor, error) {
						return authz.NewUserActor(test.actorID), nil
					}),
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			router := gin.New()
			router.GET("/", middleware, func(c *gin.Context) {
				handlerCalled = true
				c.Status(http.StatusNoContent)
			})
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			for _, header := range test.headers {
				request.Header.Add("Authorization", header)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if handlerCalled {
				t.Fatal("protected handler ran after bearer failure")
			}
			if response.Code != http.StatusUnauthorized {
				t.Fatalf(
					"status = %d body=%s",
					response.Code,
					response.Body.String(),
				)
			}
			if response.Header().Get("WWW-Authenticate") !=
				bearerTokenType {
				t.Fatalf(
					"WWW-Authenticate = %q",
					response.Header().Get("WWW-Authenticate"),
				)
			}
			if !strings.HasPrefix(
				response.Header().Get("Content-Type"),
				httpx.ProblemMediaType,
			) {
				t.Fatalf(
					"Content-Type = %q",
					response.Header().Get("Content-Type"),
				)
			}
			var problem httpx.Problem
			if err := json.Unmarshal(
				response.Body.Bytes(),
				&problem,
			); err != nil {
				t.Fatal(err)
			}
			if problem.Code != "AUTHENTICATION_REQUIRED" ||
				problem.Status != http.StatusUnauthorized {
				t.Fatalf("problem = %+v", problem)
			}
			if strings.Contains(
				response.Body.String(),
				pair.AccessToken.Value,
			) {
				t.Fatal("bearer token leaked into failure response")
			}
		})
	}
}

func TestBearerHelpersRejectInvalidConfigurationAndHeaderShapes(
	t *testing.T,
) {
	if _, err := NewBearerMiddleware(
		BearerMiddlewareConfig{},
	); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf(
			"missing service error = %v, want ErrInvalidConfig",
			err,
		)
	}
	if _, err := NewBearerMiddleware(
		BearerMiddlewareConfig{Service: &Service{}},
	); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf(
			"zero service error = %v, want ErrInvalidConfig",
			err,
		)
	}

	service := newTestService(
		t,
		openTokenDatabase(t),
		newFakeClock(
			time.Date(2026, 7, 31, 15, 0, 0, 0, time.UTC),
		),
		newDeterministicReader(71),
	)
	var resolver *testBearerActorResolver
	if _, err := NewBearerMiddleware(
		BearerMiddlewareConfig{
			Service: service,
			Actors:  resolver,
		},
	); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf(
			"typed nil resolver error = %v, want ErrInvalidConfig",
			err,
		)
	}

	for _, header := range []string{
		"",
		"Bearer",
		"Bearer ",
		" Bearer token",
		"Bearer token ",
		"Basic token",
		"Bearer token,second",
		"Bearer\ttoken",
	} {
		if _, err := AccessTokenFromAuthorization(
			header,
		); !errors.Is(err, ErrInvalidAccess) {
			t.Errorf(
				"header %q error = %v, want ErrInvalidAccess",
				header,
				err,
			)
		}
	}
	if token, err := AccessTokenFromAuthorization(
		"bEaReR token",
	); err != nil || token != "token" {
		t.Fatalf("case-insensitive bearer token=%q error=%v", token, err)
	}
}

type testBearerActorResolver struct{}

func (*testBearerActorResolver) ResolveBearerActor(
	_ context.Context,
	_ AuthenticatedSubject,
) (authz.Actor, error) {
	return authz.Actor{}, nil
}
