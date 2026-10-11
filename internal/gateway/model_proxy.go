package gateway

import (
	"encoding/json"
	"net/http"

	"deck2/internal/registry"
)

// ModelCommand is the command whose rules a call of a model through the proxy goes by.
const ModelCommand = registry.Builtin + ".model_call"

// proxyModel serves a script's call of a model: the rules of deck.model_call decide whether it goes; one that would
// wait for the owner is refused, one that goes is streamed through the hops.
func (g *Gateway) proxyModel(w http.ResponseWriter, r *http.Request, act registry.Actor, connection, path string, body []byte) {
	if g.cfg.Models == nil {
		http.Error(w, "deck: no models", http.StatusNotFound)
		return
	}
	ask, err := AskModel(connection, r.Method, path, r.Header, body)
	if err != nil {
		http.Error(w, err.Error(), registry.Status(err))
		return
	}
	hops, err := g.cfg.Models(r.Context(), act, ask)
	if err != nil {
		http.Error(w, err.Error(), registry.Status(err))
		return
	}
	c, err := g.reg.Lookup(r.Context(), act.Workspace, ModelCommand)
	if err != nil {
		http.Error(w, err.Error(), registry.Status(err))
		return
	}
	in := map[string]string{"connection": connection, "path": path}
	if ask.ByHeader || ask.InForm {
		in["model"] = ask.Model
	}
	if r.Method != http.MethodPost {
		in["method"] = r.Method
	}
	if len(body) > 0 {
		in["body"] = string(body)
	}
	input, _ := json.Marshal(in)
	now, err := g.gate(WithRoute(r.Context(), hops), act, c, act.ProjectID, input)
	if err == nil && !now {
		err = registry.Refuse("deck: a draft or a trial calls through $DECK_MODELS only free models and those of the owner's machine; this call was not made: the owner enables the project, or an agent asks with deck.model_call, which waits for the owner's yes")
	}
	if err != nil {
		g.journal(r.Context(), act, act.ProjectID, ModelCommand, c.Class, "rejected", Canonical(input), err.Error())
		http.Error(w, err.Error(), registry.Status(err))
		return
	}
	ServeHops(w, r, path, body, ask.InBody, hops)
}
