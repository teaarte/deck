package registry

import (
	"context"
	"encoding/json"
)

// Picture is a picture a result shows a model: the gateway gives it as an image beside the result's JSON.
type Picture struct {
	Type string
	Data []byte
}

// Pictured is a result with pictures for a model; its JSON names them by address for the other callers.
type Pictured interface{ Pictures() []Picture }

type picturesKey struct{}

// WithPictures marks a call whose caller takes the pictures of its result; Shows says a call is one.
func WithPictures(ctx context.Context) context.Context {
	return context.WithValue(ctx, picturesKey{}, true)
}

func Shows(ctx context.Context) bool { return ctx.Value(picturesKey{}) != nil }

// pictured is a result cut to some fields that keeps the pictures of the whole.
type pictured struct {
	v    any
	pics []Picture
}

func (p pictured) MarshalJSON() ([]byte, error) { return json.Marshal(p.v) }

func (p pictured) Pictures() []Picture { return p.pics }
