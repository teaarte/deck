package work

import (
	"context"
	"slices"

	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/provider"
	"deck2/internal/store/db"
)

// pool is what one read or one pick asks of a workspace, each once: the providers that are on, why one takes no
// work, and the projects with their manifests and grants.
type pool struct {
	s         *Service
	workspace int64
	all       []provider.Provider
	allErr    error
	allRead   bool
	off       map[string]string
	projects  map[int64]projectOf
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
	return &pool{s: s, workspace: workspace, off: map[string]string{}, projects: map[int64]projectOf{}}
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
