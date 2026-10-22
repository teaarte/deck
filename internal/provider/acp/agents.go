package acp

import "deck2/internal/provider"

// Launch is what a run gives the profile of an agent to start it from.
type Launch struct {
	Binary string
	// ModelURL is deck's proxy to the model API; the agent sends Key, the proxy puts the real key in.
	ModelURL, Model, Key string
}

// Agent is how one ACP agent starts on a model of deck, alone in its home.
type Agent struct {
	Start func(l Launch) (argv, env []string)
	// Skills is the folder in the home where the agent finds skills, so none is written in the folder it works in.
	Skills string
}

// Agents are the ACP agents deck starts; docs/spikes/acp.md has what each one does.
var Agents = map[string]Agent{"goose": {Start: goose, Skills: ".agents/skills"}}

// gooseTools are the built-in extensions of a session of deck: files and shell, and skills; the rest (sub-agents,
// schedules, apps) outlive or leave the turn.
const gooseTools = `{"developer": {"enabled": true, "type": "platform", "name": "developer", "description": "", "bundled": true},
 "skills": {"enabled": true, "type": "platform", "name": "skills", "description": "", "bundled": true}}`

func goose(l Launch) ([]string, []string) {
	env := []string{"GOOSE_PROVIDER=openai", "GOOSE_MODEL=" + l.Model, "OPENAI_BASE_URL=" + l.ModelURL, "OPENAI_API_KEY=" + l.Key,
		"GOOSE_MODE=auto", "GOOSE_DISABLE_KEYRING=1", "GOOSE_TELEMETRY_OFF=1", "EXTENSIONS=" + gooseTools}
	return []string{l.Binary, "acp"}, env
}

// caps are what an ACP agent has beyond every provider: a model on the owner's hardware.
func caps(noCloud bool) provider.Caps {
	if noCloud {
		return provider.Caps{provider.NoCloud}
	}
	return nil
}
