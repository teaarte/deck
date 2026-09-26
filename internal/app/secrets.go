package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"deck2/internal/registry"
)

var secretName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

func (a *App) secretsDir(workspace int64) string {
	return filepath.Join(a.Cfg.Home, "secrets", fmt.Sprint(workspace))
}

// Secret is the value of a workspace secret, read when a tool call needs it.
func (a *App) Secret(workspace int64, name string) (string, error) {
	if !secretName.MatchString(name) || strings.Trim(name, ".") == "" {
		return "", registry.Invalid("deck: bad secret name %q", name)
	}
	b, err := os.ReadFile(filepath.Join(a.secretsDir(workspace), name))
	if errors.Is(err, fs.ErrNotExist) {
		return "", registry.Invalid("deck: secret %q is not set; the owner sets it with deck.secret_set", name)
	}
	return strings.TrimRight(string(b), "\r\n"), err
}

// secretSet writes a secret file readable only by deck; the value never comes back out.
func (a *App) secretSet(ctx context.Context, act registry.Actor, in struct{ Name, Value string }) (any, error) {
	if !secretName.MatchString(in.Name) || strings.Trim(in.Name, ".") == "" {
		return nil, registry.Invalid("deck: a secret name is letters, digits, '.', '_' and '-'")
	}
	dir := a.secretsDir(act.Workspace)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(dir, ".new-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	_, err = f.WriteString(in.Value)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), filepath.Join(dir, in.Name))
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"name": in.Name}, nil
}

func (a *App) secretsList(ctx context.Context, act registry.Actor, _ struct{}) (any, error) {
	es, err := os.ReadDir(a.secretsDir(act.Workspace))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	names := []string{}
	for _, e := range es {
		if !strings.HasPrefix(e.Name(), ".") && e.Type().IsRegular() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}
