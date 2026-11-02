package registry

import (
	"context"
	"encoding/json"
)

type langKey struct{}

// WithLang is ctx of a caller who reads lang, such as ru; an empty one keeps the language ctx has.
func WithLang(ctx context.Context, lang string) context.Context {
	if lang == "" {
		return ctx
	}
	return context.WithValue(ctx, langKey{}, lang)
}

// Lang is the language the caller reads: the lang of the input of its call, else the one its client reads; empty
// for English or as written.
func Lang(ctx context.Context) string {
	lang, _ := ctx.Value(langKey{}).(string)
	return lang
}

// inputLang is ctx with the lang a call's input names.
func inputLang(ctx context.Context, raw json.RawMessage) context.Context {
	var in struct {
		Lang string `json:"lang"`
	}
	_ = json.Unmarshal(raw, &in)
	return WithLang(ctx, in.Lang)
}
