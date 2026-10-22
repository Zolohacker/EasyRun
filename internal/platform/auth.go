package platform

import "context"

type Actor struct {
	ID   string `json:"id"`
	Role string `json:"role"`
}

func (a Actor) IsAdmin() bool { return a.Role == "admin" || a.Role == "superadmin" }

type actorKey struct{}

func WithActor(ctx context.Context, actor Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, actor)
}
func ActorFrom(ctx context.Context) (Actor, bool) {
	a, ok := ctx.Value(actorKey{}).(Actor)
	return a, ok
}
