package models

import (
	"context"
	"database/sql"
	"slices"

	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/provider"
	"deck2/internal/store/db"
)

// pool is what one read or one pick asks of a workspace, each once: the providers on and off, up, there and their
// health, the assignments, and the projects with their manifests and grants.
type pool struct {
	s         *Service
	workspace int64
	all       []provider.Provider
	allErr    error
	allRead   bool
	off       map[string]string
	up        map[string]bool
	exists    map[string]bool
	health    map[string]healthOf
	assigned  map[assignedAt]assignedOf
	rows      map[assignedAt]string
	rowsErr   error
	projects  map[int64]projectOf
}

type healthOf struct {
	h   db.Health
	err error
}

type assignedAt struct {
	project       int64
	command, role string
}

type assignedOf struct {
	names []string
	err   error
}

// projectOf is a project with its manifest in force and the owner's answers to its rights, each with its error.
type projectOf struct {
	p      db.Project
	err    error
	m      *manifest.Manifest
	mErr   error
	grants manifest.Grants
	gErr   error
}

func (s *Service) pool(workspace int64) *pool {
	return &pool{s: s, workspace: workspace, off: map[string]string{}, up: map[string]bool{}, exists: map[string]bool{},
		health: map[string]healthOf{}, assigned: map[assignedAt]assignedOf{}, projects: map[int64]projectOf{}}
}

// All are the providers of the workspace that are on, in the order of providers.All; a copy the caller may reorder.
func (p *pool) All(ctx context.Context) ([]provider.Provider, error) {
	if !p.allRead {
		p.all, p.allErr = p.s.All(ctx, p.workspace)
		p.allRead = true
	}
	return slices.Clone(p.all), p.allErr
}

// Off is why a provider of the workspace takes no work, empty when it is on.
func (p *pool) Off(ctx context.Context, name string) string {
	if v, ok := p.off[name]; ok {
		return v
	}
	v := p.s.Off(ctx, p.workspace, name)
	p.off[name] = v
	return v
}

// Up says a new start may pick a provider of the workspace.
func (p *pool) Up(ctx context.Context, name string) bool {
	if v, ok := p.up[name]; ok {
		return v
	}
	v := p.s.Up(ctx, p.workspace, name)
	p.up[name] = v
	return v
}

// Exists says the workspace has a provider of the name.
func (p *pool) Exists(ctx context.Context, name string) bool {
	if v, ok := p.exists[name]; ok {
		return v
	}
	v := p.s.Exists(ctx, p.workspace, name)
	p.exists[name] = v
	return v
}

// Health is the health row of a provider of the workspace.
func (p *pool) Health(ctx context.Context, name string) (db.Health, error) {
	v, ok := p.health[name]
	if !ok {
		v.h, v.err = p.s.Health(ctx, p.workspace, name)
		p.health[name] = v
	}
	return v.h, v.err
}

// Assigned are the models of a role for a command of a project, as the package's Assigned reads them, from every
// assignment of the workspace read once; a copy.
func (p *pool) Assigned(ctx context.Context, project int64, command, role string) ([]string, error) {
	at := assignedAt{project, command, role}
	v, ok := p.assigned[at]
	if !ok {
		if p.rows == nil && p.rowsErr == nil {
			p.rows = map[assignedAt]string{}
			rows, err := p.s.St.Q.ListAssignments(ctx, p.workspace)
			for _, r := range rows {
				p.rows[assignedAt{r.ProjectID.Int64, r.Command, r.Role}] = r.Models
			}
			p.rowsErr = err
		}
		v.names, v.err = assignedBy(project, command, func(project int64, command string) (string, error) {
			if p.rowsErr != nil {
				return "", p.rowsErr
			}
			if raw, ok := p.rows[assignedAt{project, command, role}]; ok {
				return raw, nil
			}
			return "", sql.ErrNoRows
		})
		p.assigned[at] = v
	}
	return slices.Clone(v.names), v.err
}

// project is a project with its manifest in force and grants, as docs.ManifestAt and docs.Rights read them.
func (p *pool) project(ctx context.Context, id int64) projectOf {
	if v, ok := p.projects[id]; ok {
		return v
	}
	var v projectOf
	if v.p, v.err = p.s.St.Q.GetProject(ctx, id); v.err != nil {
		v.mErr, v.gErr = v.err, v.err
	} else if v.m, v.mErr = docs.Manifest(ctx, p.s.St.Q, v.p); v.mErr != nil {
		v.gErr = v.mErr
	} else {
		v.grants, v.gErr = docs.Grants(ctx, p.s.St.Q, id)
	}
	p.projects[id] = v
	return v
}
