package ghauth

import "context"

type contextKey struct{}

type credential struct {
	host  string
	token string
}

func WithToken(ctx context.Context, host, token string) context.Context {
	if token == "" {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, credential{host: host, token: token})
}

func Env(ctx context.Context, base []string) []string {
	c, ok := ctx.Value(contextKey{}).(credential)
	if !ok {
		return base
	}
	name := "GH_TOKEN"
	if c.host != "" && c.host != "github.com" {
		name = "GH_ENTERPRISE_TOKEN"
	}
	return append(base, name+"="+c.token)
}
