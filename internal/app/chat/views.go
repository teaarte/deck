package chat

import (
	"cmp"
	"context"
	"deck2/internal/events"
	"encoding/json"
	"slices"
	"strconv"

	"deck2/internal/access"
	"deck2/internal/ask"
	"deck2/internal/docs"
	"deck2/internal/files"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
	"deck2/internal/words"
)

// chatLines is how many lines of the log of a running reply the chat shows.
const chatLines = 30

// action is a button of the chat: a verb and the command it calls, or a screen it opens.
type action map[string]any

func chatAction(verb, command string, params map[string]any) action {
	return action{"verb": verb, "command": command, "params": params}
}

func openProcess(id int64) action {
	return action{"verb": "open", "screen": "process", "record": id}
}

// topicView is a topic: its last message cut short, the module it is about and the reply still going.
type topicView struct {
	ID        int64      `json:"id"`
	Title     string     `json:"title"`
	Project   string     `json:"project,omitempty"`
	Archived  bool       `json:"archived"`
	UpdatedAt string     `json:"updated_at"`
	Last      string     `json:"last,omitempty"`
	Model     string     `json:"model,omitempty"`
	Reply     *replyView `json:"reply,omitempty"`
	// Tokens are those its replies and the work they started spent; RepliedOn the model of its last reply, Logo the
	// words of the mark of that model, else of its own or the master's.
	Tokens    int64  `json:"tokens"`
	RepliedOn string `json:"replied_on,omitempty"`
	Logo      string `json:"logo,omitempty"`
	// Busy is that its reply or a process a reply started has not ended, since Since; Waits that one waits for the
	// owner.
	Busy  bool   `json:"busy,omitempty"`
	Waits bool   `json:"waits,omitempty"`
	Since string `json:"since,omitempty"`
}

// replyView is the process that answers a message, with its state and a button that opens it.
type replyView struct {
	Process  int64  `json:"process"`
	State    string `json:"state"`
	Provider string `json:"provider"`
	Error    string `json:"error,omitempty"`
	Open     action `json:"open"`
	process.Shown
	// Cut is what a reply that broke off left, with its undo and the providers it can go again on.
	Cut *cutView `json:"cut,omitempty"`
}

type messageView struct {
	ID      int64           `json:"id"`
	Role    string          `json:"role"`
	Text    string          `json:"text"`
	Screen  json.RawMessage `json:"screen,omitempty"`
	Process int64           `json:"process,omitempty"`
	Reply   *replyView      `json:"reply,omitempty"`
	Files   []fileView      `json:"files,omitempty"`
	// Work is how the reply went, on the last message of the master of each reply.
	Work *workView `json:"work,omitempty"`
	// Told is how a work started from the topic went, on a message of deck.
	Told      *toldView `json:"told,omitempty"`
	CreatedAt string    `json:"created_at"`
	// Delete takes back a message of the owner no reply took yet.
	Delete action `json:"delete,omitempty"`
	// Block is the one block of the catalog a message of the master shows.
	Block *blockView `json:"block,omitempty"`
}

// fileView is a file of a message: one the owner attached, or one the reply and the processes it started made.
type fileView struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
	Size int64  `json:"size"`
	URL  string `json:"url"`
	files.Seen
}

func viewFile(f db.File) fileView {
	return fileView{ID: f.ID, Name: f.Name, Type: f.Type, Size: f.Size, URL: files.At(f.ID), Seen: files.SeenOf(f)}
}

// made are the files a reply and the processes it started kept, in order.
func (s *Service) made(ctx context.Context, p db.Process) []fileView {
	var out []fileView
	fs, _ := s.St.Q.ListMadeFiles(ctx, p.ID)
	for _, f := range fs {
		out = append(out, viewFile(f))
	}
	return out
}

type questionView struct {
	Ask       int64            `json:"ask"`
	Text      string           `json:"text"`
	Questions []map[string]any `json:"questions"`
	Answer    action           `json:"answer"`
}

type turnView struct {
	Process   int64            `json:"process"`
	State     string           `json:"state"`
	Provider  string           `json:"provider"`
	StartedAt string           `json:"started_at,omitempty"`
	Lines     []map[string]any `json:"lines"`
	Processes []procView       `json:"processes"`
	Stop      action           `json:"stop"`
	Open      action           `json:"open"`
	process.Shown
}

// chatView is the topic picked of a chat block with its messages and buttons; a topic without messages suggests
// what to ask.
type chatView struct {
	Topic    *topicView    `json:"topic,omitempty"`
	Messages []messageView `json:"messages"`
	Send     action        `json:"send"`
	Archive  action        `json:"archive,omitempty"`
	Delete   action        `json:"delete,omitempty"`
	Choose   action        `json:"choose,omitempty"`
	// Transcribe is the button that turns a recording of the owner, given as file, into text.
	Transcribe action `json:"transcribe"`
	// Suggest are what a new topic may ask of the module of the owner's screen.
	Suggest []suggestView `json:"suggest"`
}

// topicsView are the topics of a chat block, and the models the master's replies may go on with its own, which a
// topic without one takes.
type topicsView struct {
	Topics   []topicView `json:"topics"`
	Archived []topicView `json:"archived"`
	// Modules are the modules any topic of the workspace is about, - for none, with the newest topic of each, whatever
	// the topics given.
	Modules []moduleView `json:"modules"`
	Models  []string     `json:"models"`
	Model   string       `json:"model,omitempty"`
	// Notes say each model in short, such as its price and speed; Logos are the words naming the model for its mark,
	// Groups the provider it stands under, whose mark is the provider's.
	Notes  map[string]string `json:"notes"`
	Logos  map[string]string `json:"logos"`
	Groups map[string]string `json:"groups"`
}

// moduleView is a module the topics are about and its newest topic.
type moduleView struct {
	Project string `json:"project"`
	Last    int64  `json:"last"`
}

// turnPart is the turn going on in the topic picked: its lines, the questions of its reply and what it and the
// processes it started wait for the owner to decide.
type turnPartView struct {
	Turn      *turnView      `json:"turn,omitempty"`
	Questions []questionView `json:"questions"`
	Asks      any            `json:"asks"`
	// Running is the work the replies of the topic started that goes on, Recent what of it ended within the hour.
	Running []runningView `json:"running"`
	Recent  []runningView `json:"recent"`
}

// suggestView is a command of a module as a text to put in the field.
type suggestView struct {
	Icon string `json:"icon"`
	Text string `json:"text"`
}

func viewReply(p db.Process, tr func(string) string) *replyView {
	return &replyView{Process: p.ID, State: p.State, Provider: p.Provider, Error: untrusted.Cut(p.Error, 500), Open: openProcess(p.ID),
		Shown: process.Show(p, "", false, tr)}
}

func viewMessage(m db.ChatMessage, asked func(int64) bool) messageView {
	v := messageView{ID: m.ID, Role: m.Role, Text: m.Text, Process: m.ProcessID, CreatedAt: m.CreatedAt}
	if m.Role == roleOwner && m.ProcessID == 0 {
		v.Delete = chatAction("delete", registry.Deck("chat_message_delete"), map[string]any{"message": m.ID})
	}
	if m.Context != "" && m.Role != roleDeck {
		v.Screen = json.RawMessage(m.Context)
	}
	if m.Block != "" {
		v.Block = viewBlock(m, asked)
	}
	return v
}

func viewTopic(t db.ListTopicSummariesRow, tr func(string) string) topicView {
	v := topicView{ID: t.ID, Title: t.Title, Project: t.Project, Archived: t.Archived == 1,
		UpdatedAt: t.UpdatedAt, Last: untrusted.Cut(t.LastText, 200), Model: t.Model, RepliedOn: t.RepliedOn}
	if t.OpenID != 0 {
		v.Reply = viewReply(db.Process{ID: t.OpenID, Kind: process.KindAgent, State: t.OpenState, Provider: t.OpenProvider, Error: t.OpenError}, tr)
	}
	return v
}

// topics are the summaries of the topics of a workspace, newest first; with topic, that one alone.
func (s *Service) topics(ctx context.Context, workspace int64, archived bool, topic, lim int64, module string, tr func(string) string) ([]topicView, error) {
	rows, err := s.St.Q.ListTopicSummaries(ctx, db.ListTopicSummariesParams{WorkspaceID: workspace, Archived: store.Bool(archived), Topic: topic, Project: module, Lim: lim})
	if err != nil {
		return nil, err
	}
	out := make([]topicView, 0, len(rows))
	for _, t := range rows {
		out = append(out, viewTopic(t, tr))
	}
	return out, nil
}

// messages are messages of a topic with the reply each message of the owner got and the files it has.
func (s *Service) messages(ctx context.Context, topic int64, rows []db.ChatMessage, ws map[string]string, tr func(string) string) []messageView {
	replies := map[int64]*replyView{}
	files := map[int64][]fileView{}
	if fs, err := s.St.Q.ListMessageFiles(ctx, topic); err == nil {
		for _, f := range fs {
			files[f.OwnerID] = append(files[f.OwnerID], viewFile(f))
		}
	}
	s.mu.Lock()
	unstarted := s.unstarted[topic]
	s.mu.Unlock()
	last := lastOf(rows)
	out := make([]messageView, 0, len(rows))
	asked := func(id int64) bool {
		a, err := s.St.Q.GetAsk(ctx, id)
		return err == nil && a.State == ask.StateOpen
	}
	for _, r := range rows {
		m := viewMessage(r, asked)
		if last[r.ProcessID] == r.ID && r.Role == roleMaster {
			if p, err := s.St.Q.GetProcess(ctx, r.ProcessID); err == nil {
				m.Work = s.work(ctx, p, tr)
				files[r.ID] = append(files[r.ID], s.made(ctx, p)...)
			}
		}
		if r.Role == roleDeck {
			m.Told = s.told(ctx, r, ws, tr)
		}
		m.Files = files[r.ID]
		if r.Role == roleOwner && r.ProcessID == 0 && unstarted != "" {
			m.Reply = &replyView{State: process.StateError, Error: untrusted.Cut("the reply did not start: "+unstarted, 500),
				Shown: process.Show(db.Process{State: process.StateError}, "", false, tr)}
		}
		if r.Role == roleOwner && r.ProcessID != 0 {
			if _, seen := replies[r.ProcessID]; !seen {
				replies[r.ProcessID] = nil
				if p, err := s.St.Q.GetProcess(ctx, r.ProcessID); err == nil {
					replies[r.ProcessID] = viewReply(p, tr)
					replies[r.ProcessID].Cut = s.cut(ctx, p, r.ID, tr)
				}
			}
			m.Reply = replies[r.ProcessID]
		}
		out = append(out, m)
	}
	return out
}

type chatGetIn struct {
	Topic   string `opt:"" desc:"Topic id; new for none, the newest without one"`
	Project string `opt:"" desc:"The module of the screen the owner is on, whose commands a new topic suggests"`
	Lang    string `opt:"" desc:"Language of the words of the states of replies, such as ru; English without one"`
}

// words put the phrases of the chat in the owner's language.
func (s *Service) words(lang string) func(string) string {
	ws := words.In(lang)
	return func(phrase string) string { return manifest.Tr(ws, phrase) }
}

// picked is the topic a read of the chat is of: the one named, none for new, else the newest.
func (s *Service) picked(ctx context.Context, workspace int64, topic string, tr func(string) string) (*topicView, error) {
	var id int64
	switch {
	case topic == "new":
		return nil, nil
	case topic != "":
		n, err := strconv.ParseInt(topic, 10, 64)
		if err != nil {
			return nil, nil
		}
		id = n
	}
	ts, err := s.topics(ctx, workspace, false, id, 1, "", tr)
	if err != nil || len(ts) == 0 {
		return nil, err
	}
	return &ts[0], nil
}

// chat is the topic picked with its messages as a chat block shows it.
func (s *Service) chat(ctx context.Context, act registry.Actor, in chatGetIn) (chatView, error) {
	v := chatView{Messages: []messageView{}, Suggest: []suggestView{}, Send: chatAction("send", registry.Deck("chat_send"), map[string]any{}),
		Transcribe: action{"label": "Speak", "command": registry.Deck("recording_transcribe"), "input": "file"}}
	var err error
	if in.Topic, err = s.readable(ctx, act, in.Topic); err != nil {
		return v, err
	}
	tr := s.words(registry.Lang(ctx))
	t, err := s.picked(ctx, act.Workspace, in.Topic, tr)
	if err != nil {
		return v, err
	}
	if t != nil {
		v.Topic = t
		v.Send = chatAction("send", registry.Deck("chat_send"), map[string]any{"topic": t.ID})
		v.Archive = chatAction("archive", registry.Deck("chat_topic_archive"), map[string]any{"topic": t.ID, "archived": !t.Archived})
		if t.Archived {
			v.Archive["verb"] = "unarchive"
		}
		v.Choose = chatAction("model", registry.Deck("chat_topic_model_set"), map[string]any{"topic": t.ID})
		v.Choose["input"] = "model"
		v.Delete = chatAction("delete", registry.Deck("chat_topic_delete"), map[string]any{"topic": t.ID})
		v.Delete["confirm"] = true
		rows, err := s.St.Q.LastMessages(ctx, db.LastMessagesParams{TopicID: t.ID, Before: 1 << 62, Lim: 100})
		if err != nil {
			return v, err
		}
		lang := s.Lang(ctx, act.Workspace, t.ID)
		v.Messages = s.messages(ctx, t.ID, rows, words.In(lang), s.words(lang))
	}
	if len(v.Messages) == 0 {
		v.Suggest = s.suggest(ctx, act.Workspace, in.Project, registry.Lang(ctx))
	}
	return v, nil
}

type chatTopicsListIn struct {
	Lang   string `opt:"" desc:"Language of the words of the states of replies, such as ru; English without one"`
	Module string `opt:"" desc:"Only the topics about this module, - for those about none; all without one"`
}

// chatTopics are the topics of the chat with the models of the master.
func (s *Service) chatTopics(ctx context.Context, act registry.Actor, in chatTopicsListIn) (topicsView, error) {
	var v topicsView
	var err error
	tr := s.words(registry.Lang(ctx))
	if v.Topics, err = s.topics(ctx, act.Workspace, false, 0, 50, in.Module, tr); err != nil {
		return v, err
	}
	if v.Archived, err = s.topics(ctx, act.Workspace, true, 0, 20, in.Module, tr); err != nil {
		return v, err
	}
	mods, err := s.St.Q.ListTopicProjects(ctx, act.Workspace)
	if err != nil {
		return v, err
	}
	v.Modules = make([]moduleView, 0, len(mods))
	for _, m := range mods {
		v.Modules = append(v.Modules, moduleView{Project: m.Project, Last: m.LastTopic})
	}
	v.Models, v.Model = s.models(ctx, act.Workspace)
	facts := s.Facts(ctx, act.Workspace, registry.Lang(ctx))
	busy := s.busy(ctx, act.Workspace)
	for _, ts := range [][]topicView{v.Topics, v.Archived} {
		for i := range ts {
			_, ts[i].Logo, _ = facts(cmp.Or(ts[i].RepliedOn, ts[i].Model, v.Model))
			ts[i].Tokens, _ = s.St.Q.TopicTokens(ctx, ts[i].ID)
			if b, ok := busy[ts[i].ID]; ok {
				ts[i].Busy, ts[i].Waits, ts[i].Since = true, b.Waits == 1, b.Since
			}
		}
	}
	v.Notes, v.Logos, v.Groups = map[string]string{}, map[string]string{}, map[string]string{}
	for _, m := range append([]string{v.Model}, v.Models...) {
		if m == "" {
			continue
		}
		note, logo, group := facts(m)
		if note != "" {
			v.Notes[m] = note
		}
		v.Logos[m] = logo
		if group != "" {
			v.Groups[m] = group
		}
	}
	return v, nil
}

// chatTurn is the turn going on in the topic picked.
func (s *Service) chatTurn(ctx context.Context, act registry.Actor, in chatGetIn) (turnPartView, error) {
	v := turnPartView{Questions: []questionView{}, Asks: []any{}, Running: []runningView{}, Recent: []runningView{}}
	var err error
	if in.Topic, err = s.readable(ctx, act, in.Topic); err != nil {
		return v, err
	}
	tr := s.words(registry.Lang(ctx))
	t, err := s.picked(ctx, act.Workspace, in.Topic, tr)
	if err != nil || t == nil {
		return v, err
	}
	lang := s.Lang(ctx, act.Workspace, t.ID)
	tr = s.words(lang)
	v.Running, v.Recent = s.running(ctx, act.Workspace, t.ID, tr)
	if t.Reply == nil {
		return v, nil
	}
	open := t.Reply
	v.Turn = &turnView{Process: open.Process, State: open.State, Provider: open.Provider, Processes: s.children(ctx, open.Process, false, tr),
		Lines: s.turnLines(ctx, act.Workspace, open.Process, tr), Stop: chatAction("stop", registry.Deck("process_stop"), map[string]any{"process": open.Process}),
		Open: openProcess(open.Process), Shown: open.Shown}
	if p, err := s.St.Q.GetProcess(ctx, open.Process); err == nil {
		v.Turn.StartedAt = cmp.Or(p.ReopenedAt.String, p.StartedAt)
	}
	turn := []int64{open.Process}
	for _, c := range v.Turn.Processes {
		turn = append(turn, c.ID)
	}
	if v.Asks, err = s.Decisions(ctx, act, turn, lang); err != nil {
		return v, err
	}
	asks, err := s.St.Q.ListOpenAsksOfProcess(ctx, open.Process)
	if err != nil {
		return v, err
	}
	for _, q := range asks {
		if q.Kind != ask.KindQuestion {
			continue
		}
		payload, _ := ask.Decode[ask.Question](q.Payload)
		questions := []map[string]any{}
		for _, raw := range payload.Questions {
			var one map[string]any
			if json.Unmarshal(raw, &one) == nil {
				questions = append(questions, one)
			}
		}
		answer := chatAction("answer", registry.Deck("ask_answer"), map[string]any{"ask": q.ID})
		answer["input"] = "answer"
		v.Questions = append(v.Questions, questionView{Ask: q.ID, Text: q.Text, Questions: questions, Answer: answer})
	}
	if len(v.Questions) > 0 {
		v.Turn.Waits, v.Turn.Word, v.Turn.Tone = true, tr("waits for you"), "warn"
	}
	return v, nil
}

// models are the models the master's replies may go on and the one they go on without a model of the topic.
func (s *Service) models(ctx context.Context, workspace int64) ([]string, string) {
	m, err := access.Master(ctx, s.St.Q, workspace)
	if err != nil {
		return []string{}, ""
	}
	cmd, err := s.replyCommand(ctx, m.ID)
	if err != nil {
		return []string{}, ""
	}
	own := ""
	if pv, err := s.For(ctx, m.ID, cmd); err == nil {
		own = pv.Name()
	}
	return s.Able(ctx, m, cmd), own
}

// turnLines are the last calls and messages in the log of a process still open, each call with what it does.
func (s *Service) turnLines(ctx context.Context, workspace, processID int64, tr func(string) string) []map[string]any {
	out := []map[string]any{}
	lines, _ := s.Runner.Tail(processID, chatLines, "tool", "tool_update", "message")
	said := map[string][2]string{}
	for _, raw := range lines {
		var l map[string]any
		_ = json.Unmarshal(raw, &l)
		if text, ok := l["text"].(string); ok {
			l["text"] = untrusted.Cut(text, 300)
		}
		if title, _ := l["title"].(string); l["kind"] == "tool" && title != "" {
			d, ok := said[title]
			if !ok {
				d[0], d[1] = s.doing(ctx, workspace, title, tr)
				said[title] = d
			}
			l["doing"], l["act"] = d[0], d[1]
		}
		out = append(out, l)
	}
	return out
}

// suggestions is how many commands a new topic suggests.
const suggestions = 3

// suggest are the commands of a module the owner may give without input, its agents' first, by their titles in the
// owner's language; none without a module.
func (s *Service) suggest(ctx context.Context, workspace int64, project, lang string) []suggestView {
	out := []suggestView{}
	if project == "" {
		return out
	}
	p, err := s.St.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: workspace, Name: project})
	if err != nil {
		return out
	}
	m, err := docs.Manifest(ctx, s.St.Q, p)
	if err != nil {
		return out
	}
	var agents, rest []suggestView
	for _, c := range m.Commands {
		var in struct {
			Required []string `json:"required"`
		}
		_ = json.Unmarshal(c.Input, &in)
		if c.Title == "" || len(in.Required) > 0 || !slices.Contains(c.Callable(), "owner") {
			continue
		}
		it := suggestView{Icon: "player-play", Text: manifest.Tr(manifest.Words(nil, m, lang), c.Title)}
		if c.Executor.Type == "agent" {
			it.Icon = "sparkles"
			agents = append(agents, it)
		} else {
			rest = append(rest, it)
		}
	}
	out = append(append(out, agents...), rest...)
	return out[:min(len(out), suggestions)]
}

// readable is the topic a read names, refused to any but the owner, save a reply turn of the master reading the topic it
// answers, which is also the topic of its read that names none.
func (s *Service) readable(ctx context.Context, act registry.Actor, topic string) (string, error) {
	if act.Kind == registry.Owner {
		return topic, nil
	}
	if p, err := s.St.Q.GetProcess(ctx, act.ProcessID); err == nil && act.ProcessID != 0 {
		own := strconv.FormatInt(s.replyOf(ctx, p), 10)
		if own != "0" && (topic == own || topic == "") {
			if g, err := s.St.Q.GetTopic(ctx, s.replyOf(ctx, p)); err == nil && g.WorkspaceID == act.Workspace {
				return own, nil
			}
		}
	}
	return "", registry.Refuse("the master reads only the topic of the chat it answers in, by its id")
}

func (s *Service) viewsCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("chat_get", "Chat",
			"Gives the topic picked of the chat with the master as a chat block shows it: its messages, the replies they got and how they went, and its buttons; a topic without messages suggests what to ask.",
			registry.OwnerSide, s.chat).On(events.Chat, events.Processes, events.Feed, events.Collection, events.Models),
		registry.Read("chat_topics_list", "Chat topics",
			"Gives the topics of the chat with the master, the archived apart, with the models its replies may go on.",
			registry.OwnerOnly, s.chatTopics).On(events.Chat, events.Processes, events.Asks, events.Models, events.Assignments, events.Health),
		registry.Read("chat_turn_get", "Chat turn",
			"Gives the turn going on in a topic of the chat with the master: the lines of its log, the processes it started, its questions and what it waits for the owner to decide; and the work the replies of the topic started that goes on or ended within the hour.",
			registry.OwnerSide, s.chatTurn).On(events.Log, events.Asks, events.Processes),
	}
}
