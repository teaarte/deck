package providers

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"deck2/internal/gateway"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// catalog reads the price and kind of a model from OpenRouter's list of its connection; a zero is free only for a model
// of text or one named free, since a song or a picture may cost what no field of the catalog says.
func (s *Service) catalog(ctx context.Context, workspace int64, c db.Connection, model string) (db.AddModelParams, bool) {
	var out db.AddModelParams
	key := ""
	if c.KeySecret != "" {
		key, _ = s.Secret(workspace, c.KeySecret)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := gateway.SendModel(ctx, gateway.ModelTarget{URL: c.Url, API: c.Api, Key: strings.TrimSpace(key)}, gateway.ModelCall{Method: http.MethodGet, Path: "models"})
	if err != nil {
		return out, false
	}
	defer resp.Body.Close()
	var list struct {
		Data []struct {
			ID           string         `json:"id"`
			Pricing      map[string]any `json:"pricing"`
			Architecture struct {
				Output []string `json:"output_modalities"`
			} `json:"architecture"`
		} `json:"data"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&list) != nil {
		return out, false
	}
	perMillion := func(v string) sql.NullFloat64 {
		f, err := strconv.ParseFloat(v, 64)
		return sql.NullFloat64{Float64: f * 1e6, Valid: err == nil && f > 0}
	}
	for _, d := range list.Data {
		if d.ID != model {
			continue
		}
		price := func(k string) string { v, _ := d.Pricing[k].(string); return v }
		out.InputUsd, out.OutputUsd = perMillion(price("prompt")), perMillion(price("completion"))
		if f, err := strconv.ParseFloat(price("request"), 64); err == nil && f > 0 {
			out.CallUsd = sql.NullFloat64{Float64: f, Valid: true}
		}
		free := strings.HasSuffix(d.ID, ":free") || len(d.Pricing) > 0 && !slices.ContainsFunc(d.Architecture.Output, func(m string) bool { return m != "text" })
		for k := range d.Pricing {
			if f, err := strconv.ParseFloat(price(k), 64); err == nil && f != 0 {
				free = false
			}
		}
		out.Free = store.Bool(free)
		switch {
		case slices.Contains(d.Architecture.Output, "video"):
			out.Kind = RoleVideo
		case slices.Contains(d.Architecture.Output, "image"):
			out.Kind = RoleImage
		case slices.Contains(d.Architecture.Output, "audio"):
			out.Kind = RoleTTS
		case len(d.Architecture.Output) > 0:
			out.Kind = RoleText
		}
		return out, true
	}
	return out, false
}
