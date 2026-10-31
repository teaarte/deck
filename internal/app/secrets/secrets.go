// Package secrets keeps the secrets of the modules of a workspace, each module's apart, and the keys of its connections
// as files only deck reads; tools, servers and providers get a value when a call needs it, agents and scripts never.
package secrets

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"deck2/internal/access"
	"deck2/internal/docs"
	"deck2/internal/events"
	"deck2/internal/home"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/schemas"
)

// Service keeps the secrets and the keys of connections.
type Service struct {
	St   *store.Store
	Bus  *events.Bus
	Home home.Layout
	mu   sync.Mutex
}

// path is the file of a secret of a project; a name that is no secret's is refused.
func (s *Service) path(workspace int64, project, name string) (string, error) {
	if !schemas.ValidSecret(name) {
		return "", registry.Invalid("bad secret name %q", name)
	}
	if !schemas.ProjectName.MatchString(project) {
		return "", registry.Invalid("bad project name %q", project)
	}
	return filepath.Join(s.Home.Secrets(workspace), project, name), nil
}

// Read is the value of a secret of a project, read when a call needs it; a module reads only its own.
func (s *Service) Read(workspace int64, project, name string) (string, error) {
	p, err := s.path(workspace, project, name)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return "", registry.Invalid("secret %q of %s is not set; the owner sets it with deck.secret_set", name, project)
	}
	return strings.TrimRight(string(b), "\r\n"), err
}

// Has reports whether a secret of a project is set.
func (s *Service) Has(workspace int64, project, name string) bool {
	p, err := s.path(workspace, project, name)
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// Swap writes a secret only while it still holds old, the value deck read before; an unset secret holds "".
func (s *Service) Swap(workspace int64, project, name, old, value string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.path(workspace, project, name)
	if err != nil {
		return false, err
	}
	b, err := os.ReadFile(p)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if strings.TrimRight(string(b), "\r\n") != strings.TrimRight(old, "\r\n") {
		return false, nil
	}
	return true, s.write(workspace, p, value)
}

// Write stores a secret of a project as a file only deck reads, in one rename, apart from a Swap of it.
func (s *Service) Write(workspace int64, project, name, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.path(workspace, project, name)
	if err != nil {
		return err
	}
	return s.write(workspace, p, value)
}

func (s *Service) write(workspace int64, path, value string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	err := home.WriteFile(path, []byte(value))
	if err == nil {
		s.Bus.Emit(events.Event{Workspace: workspace, Topic: "secrets"})
	}
	return err
}

// Drop deletes every secret of a project, as the project is deleted.
func (s *Service) Drop(workspace int64, project string) error {
	if !schemas.ProjectName.MatchString(project) {
		return registry.Invalid("bad project name %q", project)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return os.RemoveAll(filepath.Join(s.Home.Secrets(workspace), project))
}

// Key is the API key of a connection, "" when it has none.
func (s *Service) Key(workspace int64, connection string) string {
	if !schemas.ProjectName.MatchString(connection) {
		return ""
	}
	b, _ := os.ReadFile(filepath.Join(s.Home.Keys(workspace), connection))
	return strings.TrimSpace(string(b))
}

// WriteKey keeps the API key of a connection; an empty one removes it.
func (s *Service) WriteKey(workspace int64, connection, key string) error {
	if !schemas.ProjectName.MatchString(connection) {
		return registry.Invalid("bad connection name %q", connection)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.Home.Keys(workspace), connection)
	if key == "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return home.WriteFile(path, []byte(key))
}

type secretSetIn struct {
	Project string `type:"project" desc:"The module whose tools and servers use the secret"`
	Name    string `desc:"Secret name, as in $secret.<name>"`
	Value   string `type:"secret" desc:"Secret value; it is never shown again"`
}

// secretSet writes a secret of a module in a file readable only by deck; the value never comes back out.
func (s *Service) secretSet(ctx context.Context, act registry.Actor, in secretSetIn) (any, error) {
	p, err := access.Project(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	if !schemas.ValidSecret(in.Name) {
		return nil, registry.Invalid("a secret name is letters, digits, '.', '_' and '-'")
	}
	if err := s.Write(act.Workspace, p.Name, in.Name, in.Value); err != nil {
		return nil, err
	}
	return map[string]any{"project": p.Name, "name": in.Name}, nil
}

type secretDeleteIn struct {
	Project string `type:"project" desc:"The module of the secret"`
	Name    string `desc:"Secret name"`
}

// secretDelete removes a stored secret; the tools that name it fail until it is set again.
func (s *Service) secretDelete(ctx context.Context, act registry.Actor, in secretDeleteIn) (any, error) {
	p, err := access.Project(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	path, err := s.path(act.Workspace, p.Name, in.Name)
	if err != nil {
		return nil, registry.Invalid("no secret %q the owner can delete", in.Name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err = os.Remove(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, registry.NotFound("no secret %q of %s", in.Name, p.Name)
	}
	if err != nil {
		return nil, err
	}
	s.Bus.Emit(events.Event{Workspace: act.Workspace, Topic: "secrets"})
	return map[string]any{"deleted": in.Name}, nil
}

// secretRow is a secret by name, and whether it is set; its value never leaves deck.
type secretRow struct {
	Name string `json:"name"`
	Set  bool   `json:"set"`
}

type secretsListIn struct {
	Project string `type:"project" desc:"The module whose tools and servers use the secrets"`
}

// secretsList are the secrets a module's manifest names, each with whether it is set.
func (s *Service) secretsList(ctx context.Context, act registry.Actor, in secretsListIn) ([]secretRow, error) {
	p, err := access.Project(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	m, err := docs.Manifest(ctx, s.St.Q, p)
	if err != nil {
		return nil, err
	}
	out := []secretRow{}
	for _, n := range m.Secrets() {
		out = append(out, secretRow{Name: n, Set: s.Has(act.Workspace, p.Name, n)})
	}
	return out, nil
}

// Commands are the commands of secrets.
func (s *Service) Commands() []*registry.Command {
	return []*registry.Command{
		registry.Def("secret_set", "Set a secret",
			"Stores a secret of a module for its tools (a header, or the whole URL of a private link) as a file only deck reads; no other module reads it.",
			registry.OwnerOnly, s.secretSet),
		registry.Def("secret_delete", "Delete secret",
			"Deletes a stored secret of a module; the tools that name it fail until it is set again.",
			registry.OwnerOnly, s.secretDelete),
		registry.Read("secrets_list", "Secrets",
			"Lists the secrets a module names and whether each is set, never their values.",
			registry.OwnerSide, s.secretsList).On("secrets").NoTokens(),
	}
}
