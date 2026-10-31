package providers

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"deck2/internal/app/access"
	"deck2/internal/health"
	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/internal/provider/acp"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

type modelSetIn struct {
	Connection   string  `desc:"Connection the model is reached through"`
	Model        string  `desc:"The model's name in the API, such as google/gemini-3.1-flash-lite-image"`
	Name         string  `opt:"" desc:"How deck names the model in assignments and calls; connection/model when left out"`
	Kind         string  `enum:"text|image|tts|stt|music|video|search" opt:"" desc:"What the model makes, or search for a search service such as SearXNG; a model changed without it keeps its own, a new one takes it from the catalog of the connection, else text"`
	InputUSD     float64 `json:"input_usd" opt:"" desc:"USD per million input tokens, or of the unit; a model changed without a price keeps its own, a new one takes it from the catalog of the connection"`
	OutputUSD    float64 `json:"output_usd" opt:"" desc:"USD per million output tokens; a model priced per character or second has none"`
	Unit         string  `enum:"token|char|second" opt:"" desc:"What the price per million counts: tokens, characters of the text deck sends, or seconds of a recording the API reports; a model changed without it keeps its own, a new one counts tokens"`
	CallUSD      float64 `json:"call_usd" opt:"" desc:"USD per call, for a model priced per picture, clip or request"`
	Subscription *bool   `opt:"" desc:"The owner pays the model by a subscription: deck shows what a call would cost as an estimate and leaves it out of the money spent; a model changed without it keeps its own"`
	Free         *bool   `opt:"" desc:"The model costs nothing, so a trial or a draft calls it without the owner's yes; a model changed without it or a price keeps its own, a new one takes it from the catalog of the connection"`
	Options      string  `type:"json" opt:"" desc:"Fields deck adds to every call of the model of a kind, such as {\"language\": \"ru\"} of speech to text, {\"voice\": \"…\"} of tts, or {\"effort\": \"low\"} of a model of a CLI, the one option it takes; a model changed without them keeps its own, {} clears them; a text value past 16 KB is kept as a file of the model, named {\"$file\": id}"`
	Agent        string  `opt:"" desc:"The agent that runs a text model for agent commands: claude-code or codex, the CLI on the owner's subscription, on the connection of the same name; or an ACP agent inside the sandbox of deck with a shell and files. The name is then the name of a provider. A model changed without it keeps its own"`
	// CheckMinutes give a slow model more time for its self-check.
	CheckMinutes int64 `json:"check_minutes" opt:"" desc:"Minutes the self-check of a model in an agent may take, for a slow one; deck's own ceiling when left out"`
	// TPM and Context are limits of the model a request past which deck sends to the next model.
	TPM     *int64 `json:"tpm" opt:"" desc:"Tokens a minute the model takes, such as 8000 of a free tier: a request past it goes to the next model of its assignment; 0 for none, a model changed without it keeps its own"`
	Context *int64 `json:"context" opt:"" desc:"Tokens one request of the model may hold: a request past it goes to the next model of its assignment; 0 for none, a model changed without it keeps its own"`
}

// modelSetCommand is model_set, whose call held for the owner says the model it adds or changes and what it sets.
func (s *Service) modelSetCommand() *registry.Command {
	c := registry.Def("model_set", "Set model",
		"Adds or changes a model of a connection with its kind and price; what is left out comes from the catalog of the connection. A module or the master proposes a model it needs this way, and the owner adds it with one button.",
		registry.Everyone, s.modelSet).Enum("agent", s.agentNames()...).As(registry.External)
	// A model sets prices and what is free: money, which only the owner's own yes changes.
	c.Spends = true
	c.Target = func(_ string, input json.RawMessage) string {
		var in map[string]any
		_ = json.Unmarshal(input, &in)
		name := fmt.Sprint(in["connection"], "/", in["model"])
		if n, ok := in["name"].(string); ok && n != "" {
			name = n
		}
		var sets []string
		for _, k := range slices.Sorted(maps.Keys(in)) {
			if k != "connection" && k != "model" && k != "name" {
				sets = append(sets, fmt.Sprintf("%s %v", k, in[k]))
			}
		}
		return name + " → " + cmp.Or(strings.Join(sets, ", "), "as the catalog of the connection has it")
	}
	return c
}

func (s *Service) modelSet(ctx context.Context, act registry.Actor, in modelSetIn) (any, error) {
	c, err := s.St.Q.GetConnection(ctx, db.GetConnectionParams{WorkspaceID: act.Workspace, Name: in.Connection})
	if err != nil {
		return nil, registry.NotFound("no connection %q", in.Connection)
	}
	if strings.TrimSpace(in.Model) == "" {
		return nil, registry.Invalid("a model needs its name in the API")
	}
	if c.Api == APICLI && in.Name == "" {
		base, _, _ := strings.Cut(in.Model, "[")
		in.Name = in.Connection + "-" + strings.NewReplacer(".", "-", "/", "-").Replace(base)
	}
	in.Name = cmp.Or(in.Name, in.Connection+"/"+in.Model)
	if strings.ContainsAny(in.Name, " @") {
		return nil, registry.Invalid("a model name has no spaces or @, got %q", in.Name)
	}
	if c.Api == APICLI {
		in.Agent = cmp.Or(in.Agent, c.Name)
	} else if old, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: act.Workspace, Name: in.Name}); err == nil && in.Agent == "" && old.Connection.Api == APICLI {
		return nil, registry.Invalid("%s is a model of %s; a model of connection %s takes another name, or an agent", in.Name, old.Connection.Name, c.Name)
	}
	if in.Agent != "" {
		if err := s.checkAgent(ctx, act.Workspace, c, in); err != nil {
			return nil, err
		}
		in.Kind = RoleText
	}
	m := db.AddModelParams{WorkspaceID: act.Workspace, ConnectionID: c.ID, Name: in.Name, Model: in.Model, Kind: in.Kind, Options: "{}",
		InputUsd: positive(in.InputUSD), OutputUsd: positive(in.OutputUSD), CallUsd: positive(in.CallUSD), Unit: in.Unit,
		Agent: in.Agent, CheckMinutes: in.CheckMinutes}
	unpriced := in.Free == nil && !m.InputUsd.Valid && !m.OutputUsd.Valid && !m.CallUsd.Valid
	if old, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: act.Workspace, Name: in.Name}); err == nil {
		if unpriced {
			m.InputUsd, m.OutputUsd, m.CallUsd, m.Free, unpriced = old.Model.InputUsd, old.Model.OutputUsd, old.Model.CallUsd, old.Model.Free, false
		}
		m.Kind, m.CheckMinutes, m.Unit = cmp.Or(m.Kind, old.Model.Kind), cmp.Or(m.CheckMinutes, old.Model.CheckMinutes), cmp.Or(m.Unit, old.Model.Unit)
		m.Subscription, m.Tpm, m.Context = old.Model.Subscription, old.Model.Tpm, old.Model.Context
	}
	for _, l := range []struct {
		in  *int64
		out *int64
	}{{in.TPM, &m.Tpm}, {in.Context, &m.Context}} {
		if l.in != nil && *l.in < 0 {
			return nil, registry.Invalid("a limit of a model is a number of tokens, or 0 for none")
		}
		if l.in != nil {
			*l.out = *l.in
		}
	}
	m.Unit = cmp.Or(m.Unit, UnitToken)
	if m.Unit != UnitToken && m.OutputUsd.Valid {
		return nil, registry.Invalid("a model priced per %s has one price, input_usd per million; output_usd is for tokens", m.Unit)
	}
	if c.Api != APICLI && (unpriced || m.Kind == "") {
		if got, ok := s.catalog(ctx, act.Workspace, c, in.Model); ok {
			if unpriced {
				m.InputUsd, m.OutputUsd, m.CallUsd, m.Free = got.InputUsd, got.OutputUsd, got.CallUsd, got.Free
				if cmp.Or(m.Kind, got.Kind) == RoleTTS && in.Unit == "" && m.InputUsd.Valid && !m.OutputUsd.Valid {
					m.Unit = UnitChar
				}
			}
			m.Kind = cmp.Or(m.Kind, got.Kind)
		}
	}
	if in.Free != nil {
		m.Free = store.Bool(*in.Free)
	}
	if in.Subscription != nil {
		m.Subscription = store.Bool(*in.Subscription)
	}
	if c.Api == APICLI {
		m.Subscription = 1
	}
	m.Kind = cmp.Or(m.Kind, RoleText)
	if in.Options != "" {
		var o map[string]any
		if err := json.Unmarshal([]byte(in.Options), &o); err != nil {
			return nil, registry.Invalid("options are a JSON object: %v", err)
		}
		if err := s.checkFiles(ctx, act.Workspace, in.Name, o); err != nil {
			return nil, err
		}
		m.Options = in.Options
	}
	var out db.Model
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if old, err := q.GetModel(ctx, db.GetModelParams{WorkspaceID: act.Workspace, Name: in.Name}); err == nil {
			if in.Options == "" {
				m.Options = old.Model.Options
			}
			if in.Agent == "" {
				m.Agent, m.CheckMinutes = old.Model.Agent, old.Model.CheckMinutes
			}
		}
		if twin, err := q.ModelTwin(ctx, db.ModelTwinParams{ConnectionID: c.ID, Model: m.Model, Kind: m.Kind, Options: m.Options, Name: m.Name}); err == nil {
			return registry.Invalid("%s of connection %s is already model %s; change that one, its agent too", m.Model, c.Name, twin)
		}
		if in.Agent != "" {
			if err := health.Clear(ctx, q, health.Of{Workspace: act.Workspace}, health.Model(in.Name)); err != nil {
				return err
			}
			if c.Api == APICLI {
				err = health.Clear(ctx, q, health.Of{}, health.Provider(in.Name))
			} else {
				err = process.ProviderHealth(ctx, q, in.Name, health.Checking, "the self-check of the model in its agent is about to run")
			}
			if err != nil {
				return err
			}
		}
		out, err = q.AddModel(ctx, m)
		return err
	})
	if err != nil {
		return nil, err
	}
	if out.Options, err = s.keepBig(ctx, act.Workspace, out); err != nil {
		return nil, err
	}
	if in.Agent != "" && c.Api != APICLI {
		s.startCheck(in.Name, true)
	}
	return modelView{Name: out.Name, Connection: c.Name, Model: out.Model, Kind: out.Kind, Agent: out.Agent, Options: s.shownOptions(ctx, out.Options), InputUSD: nullable(out.InputUsd),
		OutputUSD: nullable(out.OutputUsd), CallUSD: nullable(out.CallUsd), Unit: out.Unit, Free: out.Free != 0,
		Subscription: out.Subscription != 0, TPM: out.Tpm, Context: out.Context}, nil
}

// checkAgent refuses a model in an agent deck cannot run: an agent it does not know or that is not installed, a
// name that is not one of a provider, an API that is not OpenAI-compatible, a model or effort its CLI does not list.
func (s *Service) checkAgent(ctx context.Context, workspace int64, c db.Connection, in modelSetIn) error {
	cli, _ := s.builtinNamed(in.Agent).(provider.Modeled)
	switch {
	case c.Api == APICLI && in.Agent != c.Name:
		return registry.Invalid("the models of connection %s run in %s, not %s", c.Name, c.Name, in.Agent)
	case cli != nil && c.Api != APICLI:
		return registry.Invalid("a model of %s is on its own connection %s, not %s", in.Agent, in.Agent, c.Name)
	case cli == nil && acp.Agents[in.Agent].Start == nil:
		return registry.Invalid("no agent %q; deck knows %s", in.Agent, strings.Join(s.agentNames(), ", "))
	case !access.ProjectName.MatchString(in.Name) || s.builtinNamed(in.Name) != nil:
		return registry.Invalid("a model in an agent is a provider; name it with lowercase letters, digits and '-', not %q or a built-in provider", in.Name)
	case cli == nil && c.Api != "openai":
		return registry.Invalid("an agent runs a model of an OpenAI-compatible connection, not %s", c.Api)
	case in.CheckMinutes < 0 || in.CheckMinutes > 60:
		return registry.Invalid("check_minutes is 1 to 60, or 0 for deck's own ceiling")
	}
	n, err := s.St.Q.ModelInOtherWorkspace(ctx, db.ModelInOtherWorkspaceParams{Name: in.Name, WorkspaceID: workspace})
	switch {
	case err != nil:
		return err
	case n > 0:
		return registry.Invalid("another workspace has a provider %q; its health and self-check go by name, so pick another", in.Name)
	case cli != nil:
		return s.checkCLI(ctx, workspace, cli, in)
	case !installed(cmp.Or(s.ACPBinaries[in.Agent], in.Agent)):
		return registry.Invalid("%s is not installed on this machine", in.Agent)
	}
	return nil
}

// checkCLI refuses a model its CLI does not list or cannot run deck's turns on, and an effort the model does not take;
// the options of a model of a CLI are its effort alone.
func (s *Service) checkCLI(ctx context.Context, workspace int64, cli provider.Modeled, in modelSetIn) error {
	options := in.Options
	if old, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: workspace, Name: in.Name}); err == nil && options == "" {
		options = old.Model.Options
	}
	effort, err := cliEffort(cmp.Or(options, "{}"))
	if err != nil {
		return err
	}
	list, err := cli.Models(ctx)
	if err != nil {
		return registry.Invalid("%v", err)
	}
	var names []string
	for _, m := range list {
		names = append(names, m.Names[0])
		if !slices.Contains(m.Names, in.Model) {
			continue
		}
		switch {
		case m.Refused != "":
			return registry.Invalid("%s: %s", in.Model, m.Refused)
		case effort != "" && len(m.Efforts) == 0:
			return registry.Invalid("%s takes no effort in %s; leave effort out of options", in.Model, in.Agent)
		case effort != "" && !slices.Contains(m.Efforts, effort):
			return registry.Invalid("%s takes effort %s in %s, not %q", in.Model, strings.Join(m.Efforts, ", "), in.Agent, effort)
		}
		return nil
	}
	return registry.Invalid("%s lists no model %q; it lists %s", in.Agent, in.Model, strings.Join(names, ", "))
}

// cliEffort is the effort of the options of a model of a CLI, the one field they take.
func cliEffort(options string) (string, error) {
	var o map[string]any
	if err := json.Unmarshal([]byte(options), &o); err != nil {
		return "", registry.Invalid("options are a JSON object: %v", err)
	}
	effort, ok := o["effort"].(string)
	if len(o) > 1 || len(o) == 1 && !ok {
		return "", registry.Invalid(`the options of a model of a CLI are its effort alone, such as {"effort": "low"}`)
	}
	return effort, nil
}

// agentNames are the agents a model runs in: the built-in CLIs that run other models, then the ACP agents.
func (s *Service) agentNames() []string {
	var out []string
	for _, pv := range s.Builtin {
		if _, ok := pv.(provider.Modeled); ok {
			out = append(out, pv.Name())
		}
	}
	return append(out, acp.Names()...)
}

func positive(f float64) sql.NullFloat64 { return sql.NullFloat64{Float64: f, Valid: f > 0} }

type modelRemoveIn struct {
	Name string `desc:"Model name"`
}

func (s *Service) modelRemove(ctx context.Context, act registry.Actor, in modelRemoveIn) (any, error) {
	var n int64
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		active, err := q.CountActiveOnProvider(ctx, db.CountActiveOnProviderParams{Provider: in.Name, WorkspaceID: act.Workspace})
		if err != nil {
			return err
		}
		if active > 0 {
			return registry.Invalid("%d processes still run on %s; stop them or let them finish first", active, in.Name)
		}
		if old, err := q.GetModel(ctx, db.GetModelParams{WorkspaceID: act.Workspace, Name: in.Name}); err == nil {
			if err := q.DeleteFilesOf(ctx, db.DeleteFilesOfParams{OwnerKind: "model", OwnerID: old.Model.ID}); err != nil {
				return err
			}
		}
		if n, err = q.DeleteModel(ctx, db.DeleteModelParams{WorkspaceID: act.Workspace, Name: in.Name}); err != nil || n == 0 {
			return err
		}
		if err := dropModel(ctx, q, act.Workspace, in.Name); err != nil {
			return err
		}
		return q.DeleteModelHealth(ctx, db.DeleteModelHealthParams{WorkspaceID: store.ID(act.Workspace), Name: in.Name})
	})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, registry.NotFound("no model %q", in.Name)
	}
	return map[string]any{"removed": in.Name}, nil
}
