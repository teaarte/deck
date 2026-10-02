package app

import (
	"context"
	"errors"
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
	return a.Home.Secrets(workspace)
}

// Secret is the value of a workspace secret, read when a tool call needs it.
func (a *App) Secret(workspace int64, name string) (string, error) {
	if !validSecret(name) {
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
func (a *App) secretSet(ctx context.Context, act registry.Actor, in secretSetIn) (any, error) {
	if !validSecret(in.Name) {
		return nil, registry.Invalid("deck: a secret name is letters, digits, '.', '_' and '-'")
	}
	a.secretMu.Lock()
	defer a.secretMu.Unlock()
	if err := a.writeSecret(act.Workspace, in.Name, in.Value); err != nil {
		return nil, err
	}
	return map[string]any{"name": in.Name}, nil
}

// SwapSecret writes a secret only while it still holds old, the value deck read before; an unset secret
// holds "".
func (a *App) SwapSecret(workspace int64, name, old, value string) (bool, error) {
	a.secretMu.Lock()
	defer a.secretMu.Unlock()
	if !validSecret(name) {
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
	if !validSecret(name) {
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

// secretRow is a secret by name; its value never leaves deck.
type secretRow struct {
	Name string `json:"name"`
}

func (a *App) secretsList(ctx context.Context, act registry.Actor, _ struct{}) ([]secretRow, error) {
	es, err := os.ReadDir(a.secretsDir(act.Workspace))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	var names []string
	for _, e := range es {
		if !strings.HasPrefix(e.Name(), ".") && e.Type().IsRegular() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	out := make([]secretRow, 0, len(names))
	for _, n := range names {
		out = append(out, secretRow{Name: n})
	}
	return out, nil
}

func validSecret(name string) bool {
	return secretName.MatchString(name) && strings.Trim(name, ".") != ""
}

func (a *App) secretsCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("secret_set", "Set secret",
			"Stores a secret for tool headers as a file only deck reads; agents and scripts never see it.",
			registry.OwnerOnly, a.secretSet).Changes("secrets"),
		registry.Read("secrets_list", "Secrets",
			"Lists the names of stored secrets, never their values.",
			registry.OwnerOnly, a.secretsList).On("secrets"),
	}
}
