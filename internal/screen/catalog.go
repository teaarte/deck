package screen

import (
	"encoding/json"
	"fmt"

	"deck2/internal/manifest"
	"deck2/schemas"
)

// component is what the catalog says of a component: the properties it takes and the span it has by default.
type component struct {
	props map[string]bool
	span  any
}

var components = func() map[string]component {
	var c struct {
		Components map[string]struct {
			Properties map[string]struct {
				Default any `json:"default"`
			} `json:"properties"`
		} `json:"components"`
	}
	if err := json.Unmarshal(schemas.Catalog, &c); err != nil {
		panic("catalog components: " + fmt.Sprint(err))
	}
	out := map[string]component{}
	for name, comp := range c.Components {
		x := component{props: map[string]bool{}, span: 1}
		for p, s := range comp.Properties {
			x.props[p] = true
			if p == "span" && s.Default != nil {
				x.span = s.Default
				if n, ok := s.Default.(float64); ok {
					x.span = int(n)
				}
			}
		}
		out[name] = x
	}
	return out
}()

// static are the properties of a block its component takes as the manifest writes them, but its data, its
// children and its span.
func static(b manifest.Block) map[string]any {
	raw, _ := json.Marshal(b)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	out := map[string]any{}
	for k, v := range m {
		if components[b.Type].props[k] && k != "data" && k != "children" && k != "span" {
			out[k] = v
		}
	}
	return out
}

// span is the cells of the grid of the screen a block takes: its own, or that of its component.
func span(b manifest.Block) any {
	switch {
	case b.Span.Full:
		return "full"
	case b.Span.N > 0:
		return b.Span.N
	}
	return components[b.Type].span
}
