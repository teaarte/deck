package screens

import (
	"context"
	"fmt"

	"deck2/internal/docs"
	"deck2/internal/store/db"
)

func (s Sources) Document(ctx context.Context, p db.Project, name string) (string, error) {
	text, err := docs.CurrentText(ctx, s.Store.Q, docs.Scope{Workspace: p.WorkspaceID, Project: p.ID}, docs.KindCustom, name)
	if err != nil {
		return "", err
	}
	if text == "" {
		return "", fmt.Errorf("no custom document %q", name)
	}
	return text, nil
}
