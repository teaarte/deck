package chat

import (
	"bytes"
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"deck2/internal/access"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/master"
	"deck2/schemas"
)

// blockMax is the most bytes of a block the master shows.
const blockMax = 16 << 10

// shown is a block the master shows in its reply, by the schema shown of the catalog.
type shown struct {
	Type    string         `json:"type"`
	Title   string         `json:"title,omitempty"`
	Options []shownOption  `json:"options,omitempty"`
	Input   map[string]any `json:"input,omitempty"`
	Values  map[string]any `json:"values,omitempty"`
	Submit  string         `json:"submit,omitempty"`
	Columns []shownColumn  `json:"columns,omitempty"`
	Rows    [][]any        `json:"rows,omitempty"`
	Kind    string         `json:"kind,omitempty"`
	Format  string         `json:"format,omitempty"`
	// Currency is of a chart; a column and a field of a card keep their own.
	Currency string        `json:"currency,omitempty"`
	X        []any         `json:"x,omitempty"`
	Series   []shownSeries `json:"series,omitempty"`
	Fields   []shownField  `json:"fields,omitempty"`
}

type shownOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type shownColumn struct {
	Label    string `json:"label"`
	Format   string `json:"format,omitempty"`
	Currency string `json:"currency,omitempty"`
}

type shownSeries struct {
	Label  string     `json:"label"`
	Values []*float64 `json:"values"`
}

type shownField struct {
	Label    string `json:"label"`
	Value    any    `json:"value"`
	Markdown bool   `json:"markdown,omitempty"`
	Format   string `json:"format,omitempty"`
	Currency string `json:"currency,omitempty"`
}

var shownSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	return schemas.Compile(schemas.Catalog, "mem://catalog.json#/$defs/shown")
})

// parseShown reads a block the master shows and checks it whole: by the schema shown of the catalog, the fields of a
// form as the input of a command, and the rows and series by their columns and x.
func parseShown(raw string) (shown, error) {
	var b shown
	if len(raw) > blockMax {
		return b, fmt.Errorf("a block is at most %d bytes", blockMax)
	}
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(raw))
	if err != nil {
		return b, fmt.Errorf("the block is not JSON: %v", err)
	}
	sch, err := shownSchema()
	if err != nil {
		return b, err
	}
	if err := sch.Validate(doc); err != nil {
		return b, fmt.Errorf("the block does not fit the schema shown of the catalog: %v", err)
	}
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		return b, err
	}
	switch b.Type {
	case "choice":
		seen := map[string]bool{}
		for _, o := range b.Options {
			if seen[o.Label] {
				return b, fmt.Errorf("option %q comes twice", o.Label)
			}
			seen[o.Label] = true
		}
	case "form":
		in, _ := json.Marshal(b.Input)
		if err := schemas.CheckInput(in); err != nil {
			return b, fmt.Errorf("input: %v", err)
		}
		props := manifest.ObjectOf(in).Types()
		if len(props) == 0 || len(props) > 8 {
			return b, fmt.Errorf("a form asks for 1 to 8 fields")
		}
		for name, p := range b.Input["properties"].(map[string]any) {
			field, _ := p.(map[string]any)
			items, _ := field["items"].(map[string]any)
			if field["format"] == "file" || items["format"] == "file" {
				return b, fmt.Errorf("field %q: a form in the chat takes no files; the owner attaches them to a message", name)
			}
		}
		if err := fits(b.Input, b.Values, false); err != nil {
			return b, fmt.Errorf("values: %v", err)
		}
	case "table":
		for i, r := range b.Rows {
			if len(r) != len(b.Columns) {
				return b, fmt.Errorf("row %d has %d cells for %d columns", i+1, len(r), len(b.Columns))
			}
		}
	case "chart":
		for _, s := range b.Series {
			if len(s.Values) != len(b.X) {
				return b, fmt.Errorf("series %q has %d values for %d of x", s.Label, len(s.Values), len(b.X))
			}
		}
	}
	return b, nil
}

// fits checks values by the input of a form; values filled before the owner types may leave out required fields.
func fits(input, values map[string]any, whole bool) error {
	schema := maps.Clone(input)
	if !whole {
		delete(schema, "required")
	}
	raw, _ := json.Marshal(schema)
	sch, err := schemas.Compile(raw, "mem://values.json")
	if err != nil {
		return err
	}
	if values == nil {
		values = map[string]any{}
	}
	vs, _ := json.Marshal(values)
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(vs))
	if err != nil {
		return err
	}
	return sch.Validate(doc)
}

type chatBlockShowIn struct {
	Block string `type:"json" desc:"The block, by the schema shown of the catalog: choice, form, table, chart or card with its data in place"`
}

// chatBlockShow shows a block of the catalog in the topic the reply turn answers: a message of the master that the
// final text of the turn fills in over it.
func (s *Service) chatBlockShow(ctx context.Context, act registry.Actor, in chatBlockShowIn) (any, error) {
	p, err := s.St.Q.GetProcess(ctx, act.ProcessID)
	if err != nil {
		return nil, registry.Invalid("only a reply of the master in a chat topic shows a block")
	}
	topic := s.replyOf(ctx, p)
	if topic == 0 {
		return nil, registry.Invalid("only a reply of the master in a chat topic shows a block")
	}
	b, err := parseShown(in.Block)
	if err != nil {
		return nil, registry.Invalid("%v", err)
	}
	raw, _ := json.Marshal(b)
	var id int64
	replaced := true
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if _, err := s.ownTopic(ctx, q, act, topic); err != nil {
			return err
		}
		id, err = q.ReplaceShownBlock(ctx, db.ReplaceShownBlockParams{Block: string(raw), TopicID: topic, ProcessID: p.ID, Since: p.StartedAt})
		if errors.Is(err, sql.ErrNoRows) {
			replaced = false
			var msg db.ChatMessage
			msg, err = q.AddBlockMessage(ctx, db.AddBlockMessageParams{TopicID: topic, ProcessID: p.ID, Block: string(raw), CreatedAt: store.Time(s.Now())})
			id = msg.ID
		}
		if err != nil {
			return err
		}
		return q.TouchTopic(ctx, db.TouchTopicParams{Now: store.Time(s.Now()), ID: topic})
	})
	if err != nil {
		return nil, err
	}
	note := "the owner sees the block now; end the turn with one to three lines that go over it"
	if replaced {
		note = "the block took the place of the one this turn showed before; end the turn with one to three lines that go over it"
	}
	if b.Type == "choice" || b.Type == "form" {
		note += "; the owner's answer comes as the next message of the topic, and a later turn acts on it"
	}
	return map[string]any{"message": id, "note": note}, nil
}

type chatAnswerIn struct {
	Message int64  `desc:"The message of the master whose block the owner answers"`
	Choice  string `opt:"" desc:"The option picked, of a choice"`
	Values  string `type:"json" opt:"" desc:"The fields filled, of a form, as a JSON object"`
}

// chatAnswer takes the owner's answer to the block of a message of the master: the block keeps it, and the topic
// gets it as the owner's next message, which the master's next reply takes.
func (s *Service) chatAnswer(ctx context.Context, act registry.Actor, in chatAnswerIn) (any, error) {
	m, err := s.St.Q.GetMessage(ctx, in.Message)
	if err != nil || m.Block == "" {
		return nil, registry.NotFound("no block of the master in message %d", in.Message)
	}
	if _, err := s.ownTopic(ctx, s.St.Q, act, m.TopicID); err != nil {
		return nil, err
	}
	var b shown
	if err := json.Unmarshal([]byte(m.Block), &b); err != nil {
		return nil, err
	}
	answer, text, err := s.answerOf(ctx, b, in)
	if err != nil {
		return nil, err
	}
	if len(text) > maxChatText {
		return nil, registry.Invalid("the answer is at most %d bytes", maxChatText)
	}
	if _, err := access.Master(ctx, s.St.Q, act.Workspace); err != nil {
		return nil, err
	}
	var msg db.ChatMessage
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		n, err := q.AnswerMessage(ctx, db.AnswerMessageParams{Answer: answer, At: store.At(s.Now()), ID: m.ID})
		if err != nil {
			return err
		}
		if n == 0 {
			return registry.Conflict("the block of message %d is answered already", m.ID)
		}
		if msg, err = q.AddMessage(ctx, db.AddMessageParams{CreatedAt: store.Time(s.Now()), TopicID: m.TopicID, Role: roleOwner, Text: text}); err != nil {
			return err
		}
		return q.TouchTopic(ctx, db.TouchTopicParams{Now: store.Time(s.Now()), ID: m.TopicID})
	})
	if err != nil {
		return nil, err
	}
	return s.sent(ctx, msg), nil
}

// answerOf is the answer to a block as kept and as the owner's message says it: the option picked, or each field
// filled by its title.
func (s *Service) answerOf(ctx context.Context, b shown, in chatAnswerIn) (string, string, error) {
	switch b.Type {
	case "choice":
		if !slices.ContainsFunc(b.Options, func(o shownOption) bool { return o.Label == in.Choice }) {
			return "", "", registry.Invalid("%q is not an option of the block", in.Choice)
		}
		raw, _ := json.Marshal(map[string]string{"choice": in.Choice})
		if b.Title != "" {
			return string(raw), b.Title + ": " + in.Choice, nil
		}
		return string(raw), in.Choice, nil
	case "form":
	default:
		return "", "", registry.Invalid("a %s takes no answer", b.Type)
	}
	var values map[string]any
	if err := json.Unmarshal([]byte(cmp.Or(strings.TrimSpace(in.Values), "{}")), &values); err != nil {
		return "", "", registry.Invalid("values are a JSON object: %v", err)
	}
	if err := fits(b.Input, values, true); err != nil {
		return "", "", registry.Invalid("values: %v", err)
	}
	schema, _ := json.Marshal(b.Input)
	tr := s.words(registry.Lang(ctx))
	lines := []string{}
	if b.Title != "" {
		lines = append(lines, b.Title)
	}
	for _, k := range manifest.PropertyOrder(schema) {
		v, ok := values[k]
		if !ok {
			continue
		}
		label := k
		if p, _ := b.Input["properties"].(map[string]any)[k].(map[string]any); p != nil {
			if t, _ := p["title"].(string); t != "" {
				label = t
			}
		}
		lines = append(lines, label+": "+said(v, tr))
	}
	raw, _ := json.Marshal(values)
	return string(raw), strings.Join(lines, "\n"), nil
}

// said is a value of a field as the owner's message says it.
func said(v any, tr func(string) string) string {
	switch x := v.(type) {
	case bool:
		if x {
			return tr("Yes")
		}
		return tr("No")
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			out = append(out, said(e, tr))
		}
		return strings.Join(out, ", ")
	case float64:
		return fmt.Sprint(x)
	}
	return fmt.Sprint(v)
}

// blockView is a block of a message as the chat draws it: what its type takes beside its data, its data, and the
// owner's answer once given.
type blockView struct {
	Type     string         `json:"type"`
	Title    string         `json:"title,omitempty"`
	Props    map[string]any `json:"props,omitempty"`
	Data     map[string]any `json:"data"`
	Answered *answeredView  `json:"answered,omitempty"`
}

type answeredView struct {
	At     string `json:"at"`
	Choice string `json:"choice,omitempty"`
}

// viewBlock is the block of a message in the shapes of the blocks of the catalog; its buttons answer it until the
// owner did, and then it shows the answer.
func viewBlock(m db.ChatMessage) *blockView {
	var b shown
	if err := json.Unmarshal([]byte(m.Block), &b); err != nil {
		return &blockView{Type: "card", Data: map[string]any{"state": "error", "error": err.Error()}}
	}
	v := &blockView{Type: b.Type, Title: b.Title, Data: map[string]any{"state": "ready"}}
	var answer map[string]any
	if m.AnsweredAt.Valid {
		_ = json.Unmarshal([]byte(m.Answer), &answer)
		v.Answered = &answeredView{At: m.AnsweredAt.String}
		v.Answered.Choice, _ = answer["choice"].(string)
	}
	answering := func(params map[string]any) action {
		params["message"] = m.ID
		return action{"command": registry.Deck("chat_answer"), "params": params}
	}
	switch b.Type {
	case "choice":
		opts := make([]map[string]any, 0, len(b.Options))
		for _, o := range b.Options {
			one := map[string]any{"label": o.Label}
			if o.Description != "" {
				one["description"] = o.Description
			}
			if v.Answered == nil {
				act := answering(map[string]any{"choice": o.Label})
				act["label"] = o.Label
				one["action"] = act
			}
			opts = append(opts, one)
		}
		v.Data["value"] = map[string]any{"options": opts}
	case "form":
		in, _ := json.Marshal(b.Input)
		form := map[string]any{"project": master.Name, "commands": []map[string]any{{
			"name": registry.Deck("chat_answer"), "title": b.Title, "input": b.Input, "order": manifest.PropertyOrder(in)}}}
		if len(b.Values) > 0 {
			form["values"] = b.Values
		}
		if v.Answered == nil {
			send := answering(map[string]any{})
			send["input"] = "values"
			if b.Submit != "" {
				send["label"] = b.Submit
			} else {
				send["verb"] = "answer"
			}
			form["send"] = send
		} else {
			form["values"] = answer
			form["readonly"] = slices.Collect(maps.Keys(manifest.ObjectOf(in).Types()))
		}
		v.Data["value"] = form
	case "table":
		v.Props = map[string]any{"columns": b.Columns}
		rows := make([]map[string]any, 0, len(b.Rows))
		for i, r := range b.Rows {
			rows = append(rows, map[string]any{"id": i + 1, "cells": r})
		}
		v.Data["rows"] = rows
	case "chart":
		v.Props = map[string]any{"kind": cmp.Or(b.Kind, "line"), "format": b.Format, "currency": b.Currency}
		v.Data["value"] = map[string]any{"x": b.X, "series": b.Series}
	case "card":
		v.Data["value"] = map[string]any{"fields": b.Fields}
	}
	return v
}

// shownLine tells a retelling of the topic what block a message showed and whether the owner answered it.
func shownLine(m db.ChatMessage) string {
	var b shown
	if m.Block == "" || json.Unmarshal([]byte(m.Block), &b) != nil {
		return ""
	}
	line := " [showed a " + b.Type
	if b.Title != "" {
		line += fmt.Sprintf(" %q", b.Title)
	}
	if m.AnsweredAt.Valid {
		line += ", answered later"
	}
	return line + "]"
}
