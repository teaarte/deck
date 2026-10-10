// Package secrets keeps the secrets of a workspace as files only deck reads; tools, servers and providers get a
// value when a call needs it, agents and scripts never.
package secrets

import (
	"context"
	"deck2/internal/app/access"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"deck2/internal/docs"
	"deck2/internal/events"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store"
)

// Service keeps the secrets; Dir is the folder of the secrets of a workspace.
type Service struct {
	St  *store.Store
	Bus *events.Bus
	Dir func(workspace int64) string
	// Set tells that a secret now has a value.
	Set func(workspace int64, name string)
	mu  sync.Mutex
}

// Read is the value of a secret of the owner, read when a call needs it; deck's own keys live elsewhere.
func (s *Service) Read(workspace int64, name string) (string, error) {
	if !access.ValidSecret(name) {
		return "", registry.Invalid("deck: bad secret name %q", name)
	}
	b, err := os.ReadFile(filepath.Join(s.Dir(workspace), name))
	if errors.Is(err, fs.ErrNotExist) {
		return "", registry.Invalid("deck: secret %q is not set; the owner sets it with deck.secret_set", name)
	}
	return strings.TrimRight(string(b), "\r\n"), err
}

// Has reports whether a secret is set.
func (s *Service) Has(workspace int64, name string) bool {
	_, err := os.Stat(filepath.Join(s.Dir(workspace), name))
	return err == nil
}

type secretSetIn struct {
	Name  string `desc:"Secret name, as in $secret.<name>"`
	Value string `type:"secret" desc:"Secret value; it is never shown again"`
}

// secretSet writes a secret file readable only by deck; the value never comes back out.
func (s *Service) secretSet(ctx context.Context, act registry.Actor, in secretSetIn) (any, error) {
	if !access.ValidSecret(in.Name) {
		return nil, registry.Invalid("deck: a secret name is letters, digits, '.', '_' and '-'")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.Write(act.Workspace, in.Name, in.Value); err != nil {
		return nil, err
	}
	return map[string]any{"name": in.Name}, nil
}

type secretDeleteIn struct {
	Name string `desc:"Secret name"`
}

// secretDelete removes a stored secret; tools and providers that name it fail until it is set again.
func (s *Service) secretDelete(ctx context.Context, act registry.Actor, in secretDeleteIn) (any, error) {
	if !access.ValidSecret(in.Name) {
		return nil, registry.Invalid("deck: no secret %q the owner can delete", in.Name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err := os.Remove(filepath.Join(s.Dir(act.Workspace), in.Name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, registry.NotFound("deck: no secret %q", in.Name)
	}
	if err != nil {
		return nil, err
	}
	s.Bus.Emit(events.Event{Workspace: act.Workspace, Topic: "secrets"})
	return map[string]any{"deleted": in.Name}, nil
}

// Swap writes a secret only while it still holds old, the value deck read before; an unset secret
// holds "".
func (s *Service) Swap(workspace int64, name, old, value string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !access.ValidSecret(name) {
		return false, registry.Invalid("deck: bad secret name %q", name)
	}
	b, err := os.ReadFile(filepath.Join(s.Dir(workspace), name))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if strings.TrimRight(string(b), "\r\n") != strings.TrimRight(old, "\r\n") {
		return false, nil
	}
	return true, s.Write(workspace, name, value)
}

// Write stores a secret as a file only deck reads, in one rename.
func (s *Service) Write(workspace int64, name, value string) error {
	if !access.ValidSecret(name) {
		return registry.Invalid("deck: bad secret name %q", name)
	}
	err := provider.WriteFile(filepath.Join(s.Dir(workspace), name), []byte(value))
	if err == nil {
		s.Set(workspace, name)
		s.Bus.Emit(events.Event{Workspace: workspace, Topic: "secrets"})
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

func (s *Service) secretsList(ctx context.Context, act registry.Actor, in secretsListIn) ([]secretRow, error) {
	if in.Project != "" {
		return s.projectSecrets(ctx, act, in.Project)
	}
	es, err := os.ReadDir(s.Dir(act.Workspace))
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
		out = append(out, secretRow{Name: n, Set: true})
	}
	return out, nil
}

// projectSecrets are the secrets a project's manifest names, each with whether the workspace has it.
func (s *Service) projectSecrets(ctx context.Context, act registry.Actor, project string) ([]secretRow, error) {
	p, err := access.Project(ctx, s.St.Q, act, project)
	if err != nil {
		return nil, err
	}
	m, err := docs.Manifest(ctx, s.St.Q, p)
	if err != nil {
		return nil, err
	}
	out := []secretRow{}
	for _, n := range m.Secrets() {
		out = append(out, secretRow{Name: n, Set: s.Has(act.Workspace, n)})
	}
	return out, nil
}

// Commands are the commands of secrets.
func (s *Service) Commands() []*registry.Command {
	return []*registry.Command{
		registry.Def("secret_set", "Set secret",
			"Stores a secret for tool headers as a file only deck reads; agents and scripts never see it.",
			registry.OwnerOnly, s.secretSet),
		registry.Def("secret_delete", "Delete secret",
			"Deletes a stored secret; the tools and providers that name it fail until it is set again.",
			registry.OwnerOnly, s.secretDelete),
		registry.Read("secrets_list", "Secrets",
			"Lists the names of stored secrets, never their values.",
			registry.OwnerSide, s.secretsList).On("secrets"),
	}
}
