package access

import (
	"errors"
	"testing"

	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

func TestWithin(t *testing.T) {
	for _, row := range []struct {
		actor   registry.Actor
		project int64
		may     bool
		what    string
	}{
		{registry.Actor{Kind: registry.Owner}, 9, true, "the owner, any project"},
		{registry.Actor{Kind: registry.Master, ProjectID: 1}, 9, true, "the master, any project"},
		{registry.Actor{Kind: registry.Agent, ProjectID: 7}, 7, true, "an agent, its own project"},
		{registry.Actor{Kind: registry.Agent, ProjectID: 7}, 8, false, "an agent, another project"},
		{registry.Actor{Kind: registry.Trigger, ProjectID: 7}, 7, true, "a trigger, its own project"},
		{registry.Actor{Kind: registry.Trigger, ProjectID: 7}, 8, false, "a trigger, another project"},
		{registry.Actor{Kind: registry.Agent}, 7, false, "an agent of no project"},
		{registry.Actor{Kind: registry.Token, Scope: registry.Scope{Reads: []int64{7}}}, 7, true, "a token reads a module its data names"},
		{registry.Actor{Kind: registry.Token, Scope: registry.Scope{Reads: []int64{7}}}, 8, false, "a token reads no module its data does not name"},
		{registry.Actor{Kind: registry.Token, Changes: true, Scope: registry.Scope{Reads: []int64{7}}}, 7, false, "a token changes no module it only reads"},
		{registry.Actor{Kind: registry.Token, Changes: true, Scope: registry.Scope{Acts: []int64{7}}}, 7, true, "a token changes a module named whole"},
		{registry.Actor{Kind: registry.Token, Scope: registry.Scope{Every: true}}, 7, false, "every module of a token is the list deck made of it"},
		{registry.Actor{Kind: registry.Token}, 0, false, "a token, no project"},
	} {
		if got := Within(row.actor, row.project) == nil; got != row.may {
			t.Errorf("Within(%+v, %d) = %v, want %v: %s", row.actor, row.project, got, row.may, row.what)
		}
	}
}

func Test_mayRead(t *testing.T) {
	notes := db.Project{ID: 7, Name: "notes"}
	diary := db.Project{ID: 8, Name: "diary"}
	master := db.Project{ID: 1, Name: "master", Builtin: 1}
	agent := registry.Actor{Kind: registry.Agent, ProjectID: 7}
	declares := func(what ...string) *manifest.Manifest {
		return &manifest.Manifest{Rights: []manifest.Declared{{Right: manifest.Data, What: what}}}
	}
	always := func(subject string) manifest.Grants {
		return manifest.Grants{{Right: manifest.Data, Who: "", Subject: subject, Answer: manifest.Always}}
	}
	broken := errors.New("no manifest")
	for _, row := range []struct {
		actor                registry.Actor
		p                    db.Project
		collection, document string
		m                    *manifest.Manifest
		g                    manifest.Grants
		err                  error
		may                  bool
		what                 string
	}{
		{registry.Actor{Kind: registry.Owner}, diary, "", "", nil, nil, nil, true, "the owner reads any module"},
		{registry.Actor{Kind: registry.Owner}, master, "", "memory/a.md", nil, nil, nil, true, "the owner reads the memory"},
		{registry.Actor{Kind: registry.Master, ProjectID: 1}, diary, "items", "", nil, nil, nil, true, "the master reads a collection of any module"},
		{agent, notes, "items", "", nil, nil, nil, true, "an agent reads its own collection"},
		{agent, notes, "", "memory/a.md", nil, nil, nil, true, "an agent reads its own memory"},
		{agent, diary, "", "memory/a.md", declares("diary"), always("diary"), nil, false, "an agent reads no memory of another module, even granted"},
		{agent, master, "", "", declares("*"), always("*"), nil, false, "an agent reads nothing of the master, even granted every module"},
		{agent, master, "topics", "", declares("*"), always("*"), nil, false, "an agent reads no collection of the master"},
		{agent, diary, "items", "", declares("diary.items"), always("diary.items"), nil, true, "a collection declared and granted"},
		{agent, diary, "items", "", declares("diary"), always("diary"), nil, true, "a collection of a module declared and granted whole"},
		{agent, diary, "items", "", declares("*"), always("*"), nil, true, "a collection of every module granted"},
		{agent, diary, "items", "", declares("diary.items"), nil, nil, false, "a collection declared, not granted"},
		{agent, diary, "items", "", declares("diary.other"), always("diary.other"), nil, false, "another collection granted"},
		{agent, diary, "items", "", declares("dia"), always("dia"), nil, false, "a module whose name starts the same"},
		{agent, diary, "items", "", declares("diary.items"), manifest.Grants{{Right: manifest.Data, Subject: "diary.items", Answer: manifest.No}}, nil, false, "a collection the owner said no to"},
		{agent, diary, "", "notes/a.md", declares("diary/notes/a.md"), always("diary/notes/a.md"), nil, true, "a document declared and granted"},
		{agent, diary, "", "notes/a.md", declares("diary"), nil, nil, false, "a document of a module declared, not granted"},
		{agent, diary, "", "", declares("diary"), always("diary"), nil, true, "a module granted whole"},
		{agent, diary, "", "", declares("diary.items"), always("diary.items"), nil, true, "a module one of whose collections is granted"},
		{agent, diary, "", "", declares("notes2"), always("notes2"), nil, false, "a module granted another"},
		{agent, diary, "", "", nil, nil, broken, false, "a manifest that does not load"},
		{agent, diary, "items", "", nil, nil, broken, false, "a manifest that does not load, a collection"},
		{registry.Actor{Kind: registry.Token, Scope: registry.Scope{Reads: []int64{8}}}, diary, "items", "", nil, nil, nil, true, "a token reads a module its data names"},
		{registry.Actor{Kind: registry.Token, Scope: registry.Scope{Reads: []int64{8}}}, notes, "items", "", declares("*"), always("*"), nil, false, "a token reads no other module, whatever its own module holds"},
		{registry.Actor{Kind: registry.Token, Scope: registry.Scope{Reads: []int64{1}}}, master, "", "memory/a.md", nil, nil, nil, true, "a token that names the master reads its memory"},
	} {
		load := func() (*manifest.Manifest, manifest.Grants, error) {
			if row.err != nil {
				return nil, nil, row.err
			}
			return row.m, row.g, nil
		}
		if got := mayRead(row.actor, row.p, row.collection, row.document, load) == nil; got != row.may {
			t.Errorf("mayRead(%s %d, %s, %q, %q) = %v, want %v: %s", row.actor.Kind, row.actor.ProjectID, row.p.Name, row.collection, row.document, got, row.may, row.what)
		}
	}
}
