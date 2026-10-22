package documents

import (
	"context"
	"deck2/internal/docs"
)

// Text is the current text of a project document by kind and name, of any kind for ""; empty when missing.
func (dc *Service) Text(ctx context.Context, projectID int64, kind, name string) (string, error) {
	p, err := dc.St.Q.GetProject(ctx, projectID)
	if err != nil {
		return "", err
	}
	return docs.CurrentText(ctx, dc.St.Q, docs.ProjectScope(p), kind, name)
}

// Prompt is the current md prompt of a project; empty when it has none.
func (dc *Service) Prompt(ctx context.Context, projectID int64) (string, error) {
	p, err := dc.St.Q.GetProject(ctx, projectID)
	if err != nil {
		return "", err
	}
	return docs.CurrentText(ctx, dc.St.Q, docs.ProjectScope(p), docs.KindPrompt, docs.PromptName)
}
