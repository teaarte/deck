package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"deck2/internal/provider"
)

// Models are the models the CLI lists in its answer to initialize; no model is called. A model without auto mode
// is refused: deck's turns run in it.
func (a *Adapter) Models(ctx context.Context) ([]provider.Model, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := os.MkdirAll(a.ConfigDir, 0o700); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, a.Binary, "-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--strict-mcp-config", "--setting-sources", "")
	cmd.Dir = a.ConfigDir
	cmd.Env = environ()
	cmd.Stdin = strings.NewReader(`{"type":"control_request","request_id":"deck-models","request":{"subtype":"initialize"}}` + "\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s lists no models: %w: %s", a.Binary, err, strings.TrimSpace(stderr.String()))
	}
	for line := range bytes.Lines(out) {
		var l struct {
			Response struct {
				RequestID string `json:"request_id"`
				Response  struct {
					Models []struct {
						Value            string   `json:"value"`
						ResolvedModel    string   `json:"resolvedModel"`
						SupportedEfforts []string `json:"supportedEffortLevels"`
						SupportsAutoMode bool     `json:"supportsAutoMode"`
					} `json:"models"`
				} `json:"response"`
			} `json:"response"`
		}
		if json.Unmarshal(line, &l) != nil || l.Response.RequestID != "deck-models" {
			continue
		}
		var list []provider.Model
		for _, m := range l.Response.Response.Models {
			if m.Value == "default" {
				continue
			}
			v := provider.Model{Efforts: m.SupportedEfforts}
			for _, n := range []string{m.Value, m.ResolvedModel} {
				base, _, _ := strings.Cut(n, "[")
				v.Names = append(v.Names, n, base)
			}
			if !m.SupportsAutoMode {
				v.Refused = "Claude Code has no auto mode on this model, and deck's turns run in auto mode"
			}
			list = append(list, v)
		}
		return list, nil
	}
	return nil, fmt.Errorf("%s did not answer initialize with its models", a.Binary)
}
