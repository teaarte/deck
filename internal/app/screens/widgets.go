package screens

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"deck2/internal/app/access"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// widgetRole is the role of the models that draw a view of the card of a module; the master's model without one.
const widgetRole = "widget"

type widgetDrawIn struct {
	Project string `type:"project" desc:"The module whose card the view is for"`
	Size    string `desc:"The size of the card, columns x rows of cells, such as 4x2"`
	Wish    string `type:"text" opt:"" desc:"What the owner wishes to see on the card"`
}

// widgetDraw tells the master to make a view of the card of a module for a size, on the model of the role widget;
// deck draws nothing itself, the view comes as a change of the manifest.
func (ss *Service) widgetDraw(ctx context.Context, act registry.Actor, in widgetDrawIn) (any, error) {
	p, err := access.Project(ctx, ss.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	if _, _, ok := manifest.Size(in.Size); !ok {
		return nil, registry.Invalid("deck: a size of cells such as 4x2, not %q", in.Size)
	}
	ws := Phrases(ss.Language(ctx, act.Workspace))
	b := []string{manifest.TrText(ws, fmt.Sprintf("Make a view of the card of module %s on the tiles for the size %s, columns by rows of cells: a widget of its manifest with min %s, of stat, list and chart blocks, that shows on this size what matters most of the module, by the skill widgets; save it with manifest_save.", p.Name, in.Size, in.Size))}
	if w := strings.TrimSpace(in.Wish); w != "" {
		b = append(b, manifest.TrText(ws, fmt.Sprintf("The owner wishes: %s", w)))
	}
	send := map[string]any{"text": strings.Join(b, "\n\n")}
	// The model of the role for the module, else for the workspace.
	for _, at := range []int64{p.ID, 0} {
		raw, err := ss.St.Q.GetAssignment(ctx, db.GetAssignmentParams{WorkspaceID: act.Workspace, ProjectID: at, Role: widgetRole})
		var models []string
		if err == nil && json.Unmarshal([]byte(raw), &models) == nil && len(models) > 0 {
			send["model"] = models[0]
			break
		}
	}
	input, _ := json.Marshal(send)
	return ss.Registry.Invoke(ctx, act, registry.Builtin+".chat_send", input)
}
