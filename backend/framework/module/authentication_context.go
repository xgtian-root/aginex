package module

import (
	"context"
	"fmt"
	"strings"

	"github.com/xgtian-root/aginex/backend/framework/authz"
)

type authenticatedActorContextKey struct{}

// ContextWithAuthenticatedActor attaches the actor established by a registered
// authentication scheme. Authentication middleware must call this before
// invoking the next handler.
func ContextWithAuthenticatedActor(
	ctx context.Context,
	actor authz.Actor,
) (context.Context, error) {
	if ctx == nil || !validAuthenticatedActor(actor) {
		return nil, fmt.Errorf(
			"%w: invalid authenticated actor",
			ErrInvalid,
		)
	}
	actor.Grants = append([]authz.Grant(nil), actor.Grants...)
	return context.WithValue(
		ctx,
		authenticatedActorContextKey{},
		actor,
	), nil
}

// AuthenticatedActorFromContext returns a copy of the actor established by the
// operation's registered authentication scheme.
func AuthenticatedActorFromContext(ctx context.Context) (authz.Actor, bool) {
	if ctx == nil {
		return authz.Actor{}, false
	}
	actor, ok := ctx.Value(authenticatedActorContextKey{}).(authz.Actor)
	if !ok || !validAuthenticatedActor(actor) {
		return authz.Actor{}, false
	}
	actor.Grants = append([]authz.Grant(nil), actor.Grants...)
	return actor, true
}

func validAuthenticatedActor(actor authz.Actor) bool {
	return strings.TrimSpace(actor.ID) != "" &&
		(actor.Kind == authz.ActorKindUser ||
			actor.Kind == authz.ActorKindSystem)
}
