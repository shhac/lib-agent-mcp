package oauth

import "context"

// principalKey keys the Verified identity Protect attaches to the request
// context.
type principalKey struct{}

// WithPrincipal returns ctx carrying the validated token identity. Protect
// calls it on every authorized request; it is exported so embedders and tests
// can construct principal-bearing contexts directly.
func WithPrincipal(ctx context.Context, v Verified) context.Context {
	return context.WithValue(ctx, principalKey{}, v)
}

// PrincipalFrom returns the identity Protect attached to ctx, if any. Tool
// dispatch reads it to bind a caller to per-principal credentials; absence
// means the transport had no OAuth gate (stdio, or plain HTTP).
func PrincipalFrom(ctx context.Context) (Verified, bool) {
	v, ok := ctx.Value(principalKey{}).(Verified)
	return v, ok
}
