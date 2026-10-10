package gateway

import (
	"encoding/json"
	"net/http"

	"deck2/internal/registry"
)

// ModelRequest is the command a call of a model through the proxy is frozen as when it waits for the owner.
const ModelRequest = registry.Builtin + ".model_request"

// proxyModel serves a script's call of a model: the rules of deck.model_request decide whether it goes now or
// waits for the owner, frozen as that command; one that goes is streamed through the hops.
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
	c, err := g.reg.Lookup(r.Context(), act.Workspace, ModelRequest)
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
	if err != nil {
		g.journal(r.Context(), act, act.ProjectID, ModelRequest, c.Class, "rejected", Canonical(input), err.Error())
		http.Error(w, err.Error(), registry.Status(err))
		return
	}
	if !now {
		if len(body) > 0 && !json.Valid(body) {
			http.Error(w, "deck: this call of a model waits for the owner, and only a JSON body can wait; call it again once the owner enabled the project", http.StatusTooManyRequests)
			return
		}
		out, err := g.hold(r.Context(), act, c, act.ProjectID, input)
		if err != nil {
			http.Error(w, err.Error(), registry.Status(err))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(out)
		return
	}
	ServeHops(w, r, path, body, ask.InBody, hops)
}
