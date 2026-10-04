package app

import (
	"context"
	"deck2/internal/app/access"
	"deck2/internal/app/devices"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/registry"
)

func (a *App) secretsDir(workspace int64) string {
	return a.Home.Secrets(workspace)
}

// Secret is the value of a workspace secret, read when a tool call needs it.
func (a *App) Secret(workspace int64, name string) (string, error) {
	if slices.Contains(serviceSecrets, name) {
		return "", registry.Invalid("deck: %s is deck's own key; no tool, server or provider gets it", name)
	}
	return a.readSecret(workspace, name)
}

func (a *App) readSecret(workspace int64, name string) (string, error) {
	if !access.ValidSecret(name) {
		return "", registry.Invalid("deck: bad secret name %q", name)
	}
	b, err := os.ReadFile(filepath.Join(a.secretsDir(workspace), name))
	if errors.Is(err, fs.ErrNotExist) {
		return "", registry.Invalid("deck: secret %q is not set; the owner sets it with deck.secret_set", name)
	}
	return strings.TrimRight(string(b), "\r\n"), err
}

type secretSetIn struct {
	Name  string `desc:"Secret name, as in $secret.<name>"`
	Value string `type:"secret" desc:"Secret value; it is never shown again"`
}

// secretSet writes a secret file readable only by deck; the value never comes back out.
// serviceSecrets are deck's own keys: the owner neither lists nor sets them, and resets them with their own button.
var serviceSecrets = []string{devices.VAPIDSecret}

func (a *App) secretSet(ctx context.Context, act registry.Actor, in secretSetIn) (any, error) {
	if !access.ValidSecret(in.Name) {
		return nil, registry.Invalid("deck: a secret name is letters, digits, '.', '_' and '-'")
	}
	if slices.Contains(serviceSecrets, in.Name) {
		return nil, registry.Invalid("deck: %s is deck's own key; reset it on the Interface tab of Settings", in.Name)
	}
	a.secretMu.Lock()
	defer a.secretMu.Unlock()
	if err := a.writeSecret(act.Workspace, in.Name, in.Value); err != nil {
		return nil, err
	}
	return map[string]any{"name": in.Name}, nil
}

type secretDeleteIn struct {
	Name string `desc:"Secret name"`
}

// secretDelete removes a stored secret; tools and providers that name it fail until it is set again.
func (a *App) secretDelete(ctx context.Context, act registry.Actor, in secretDeleteIn) (any, error) {
	if !access.ValidSecret(in.Name) || slices.Contains(serviceSecrets, in.Name) {
		return nil, registry.Invalid("deck: no secret %q the owner can delete", in.Name)
	}
	a.secretMu.Lock()
	defer a.secretMu.Unlock()
	err := os.Remove(filepath.Join(a.secretsDir(act.Workspace), in.Name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, registry.NotFound("deck: no secret %q", in.Name)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"deleted": in.Name}, nil
}

// SwapSecret writes a secret only while it still holds old, the value deck read before; an unset secret
// holds "".
func (a *App) SwapSecret(workspace int64, name, old, value string) (bool, error) {
	a.secretMu.Lock()
	defer a.secretMu.Unlock()
	if !access.ValidSecret(name) {
		return false, registry.Invalid("deck: bad secret name %q", name)
	}
	b, err := os.ReadFile(filepath.Join(a.secretsDir(workspace), name))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if strings.TrimRight(string(b), "\r\n") != strings.TrimRight(old, "\r\n") {
		return false, nil
	}
	return true, a.writeSecret(workspace, name, value)
}

func (a *App) writeSecret(workspace int64, name, value string) error {
	if !access.ValidSecret(name) {
		return registry.Invalid("deck: bad secret name %q", name)
	}
	dir := a.secretsDir(workspace)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".new-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.WriteString(value)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), filepath.Join(dir, name))
	}
	return err
}

// secretRow is a secret by name, and whether it is set; its value never leaves deck.
type secretRow struct {
	Name string `json:"name"`
	Set  bool   `json:"set"`
}

type secretsListIn struct {
	Project string `type:"project" opt:"" desc:"The secrets the tools and servers of this project use, set or not"`
}

func (a *App) secretsList(ctx context.Context, act registry.Actor, in secretsListIn) ([]secretRow, error) {
	if in.Project != "" {
		return a.projectSecrets(ctx, act, in.Project)
	}
	es, err := os.ReadDir(a.secretsDir(act.Workspace))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	var names []string
	for _, e := range es {
		if !strings.HasPrefix(e.Name(), ".") && e.Type().IsRegular() && !slices.Contains(serviceSecrets, e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	out := make([]secretRow, 0, len(names))
	for _, n := range names {
		out = append(out, secretRow{Name: n, Set: true})
	}
	return out, nil
}

// projectSecrets are the secrets a project's manifest names, each with whether the workspace has it.
func (a *App) projectSecrets(ctx context.Context, act registry.Actor, project string) ([]secretRow, error) {
	p, err := access.Project(ctx, a.Store.Q, act, project)
	if err != nil {
		return nil, err
	}
	m, err := docs.Manifest(ctx, a.Store.Q, p)
	if err != nil {
		return nil, err
	}
	out := []secretRow{}
	for _, n := range m.Secrets() {
		_, err := os.Stat(filepath.Join(a.secretsDir(act.Workspace), n))
		out = append(out, secretRow{Name: n, Set: err == nil})
	}
	return out, nil
}

func (a *App) secretsCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("secret_set", "Set secret",
			"Stores a secret for tool headers as a file only deck reads; agents and scripts never see it.",
			registry.OwnerOnly, a.secretSet).Changes("secrets"),
		registry.Def("secret_delete", "Delete secret",
			"Deletes a stored secret; the tools and providers that name it fail until it is set again.",
			registry.OwnerOnly, a.secretDelete).Changes("secrets"),
		registry.Read("secrets_list", "Secrets",
			"Lists the names of stored secrets, never their values.",
			registry.OwnerSide, a.secretsList).On("secrets"),
	}
}
