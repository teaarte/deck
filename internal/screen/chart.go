package screen

import (
	"fmt"
	"math"

	"deck2/internal/manifest"
)

// ChartPoints is the most points a chart draws: the last ones of its source.
const ChartPoints = 500

// chart is the data of a chart: the x of each item and, by series, its value; a value that is not a number is a gap.
func (r *renderer) chart(b manifest.Block, items []map[string]any) (map[string]any, error) {
	if len(items) > ChartPoints {
		items = items[len(items)-ChartPoints:]
	}
	xs := make([]any, 0, len(items))
	for _, it := range items {
		x, err := r.eval(b.X, it)
		if err != nil {
			return nil, fmt.Errorf("x: %w", err)
		}
		xs = append(xs, x)
	}
	series := make([]map[string]any, 0, len(b.Series))
	for _, s := range b.Series {
		values := make([]any, 0, len(items))
		for _, it := range items {
			y, err := r.eval(s.Y, it)
			if err != nil {
				return nil, fmt.Errorf("series %q: %w", s.Label, err)
			}
			if f, ok := number(y); ok && !math.IsNaN(f) && !math.IsInf(f, 0) {
				values = append(values, f)
			} else {
				values = append(values, nil)
			}
		}
		series = append(series, map[string]any{"label": s.Label, "values": values})
	}
	return map[string]any{"x": xs, "series": series}, nil
}
