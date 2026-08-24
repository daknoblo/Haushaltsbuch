package i18n

import (
	"context"
	"fmt"
)

type ctxKey struct{}

// WithLang stores the request language in the context. templ passes the
// context into every component, so views can translate without threading a
// parameter through each call.
func WithLang(ctx context.Context, lang Lang) context.Context {
	return context.WithValue(ctx, ctxKey{}, lang)
}

// LangFrom returns the language stored in ctx, or the default.
func LangFrom(ctx context.Context) Lang {
	if l, ok := ctx.Value(ctxKey{}).(Lang); ok && Supported(l) {
		return l
	}
	return Default
}

// C translates a key using the language stored in ctx.
func C(ctx context.Context, key Key) string {
	return T(LangFrom(ctx), key)
}

// Cf translates a format key and fills in the arguments.
func Cf(ctx context.Context, key Key, args ...any) string {
	return fmt.Sprintf(T(LangFrom(ctx), key), args...)
}
