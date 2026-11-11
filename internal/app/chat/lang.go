package chat

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"

	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// langCode is a language as a code, such as ru, en or pt-BR.
var langCode = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]{2,8})?$`)

// Lang is the language the owner reads what comes of a topic in: the one its reply marked, else the one the
// caller reads (a client reads that of the settings), else that of the settings; topic 0 is none.
func (s *Service) Lang(ctx context.Context, workspace, topic int64) string {
	if topic != 0 {
		if t, err := s.St.Q.GetTopic(ctx, topic); err == nil && t.Lang != "" {
			return t.Lang
		}
	}
	if l := registry.Lang(ctx); l != "" {
		return l
	}
	return s.gives(ctx, workspace, 0)
}

// LangOf is the language of what a process says to the owner: that of the topic it or a process that started it
// came from, else as Lang says.
func (s *Service) LangOf(ctx context.Context, workspace, process int64) string {
	return s.Lang(ctx, workspace, s.TopicOf(ctx, process))
}

// Gives is the language deck gives an agent of a process: that of its topic, else that of the settings; "" when the
// settings follow the device and no reply marked it, whoever's call started the process.
func (s *Service) Gives(ctx context.Context, workspace, process int64) string {
	return s.gives(ctx, workspace, s.TopicOf(ctx, process))
}

func (s *Service) gives(ctx context.Context, workspace, topic int64) string {
	if topic != 0 {
		if t, err := s.St.Q.GetTopic(ctx, topic); err == nil && t.Lang != "" {
			return t.Lang
		}
	}
	if l := s.Language(ctx, workspace); l != "auto" {
		return l
	}
	return ""
}

// TopicOf is the chat topic a process of the master came from, itself or through the processes that started it;
// 0 for none.
func (s *Service) TopicOf(ctx context.Context, process int64) int64 {
	for range 16 {
		if process == 0 {
			return 0
		}
		p, err := s.St.Q.GetProcess(ctx, process)
		if err != nil {
			return 0
		}
		if proj, err := s.St.Q.GetProject(ctx, p.ProjectID); err == nil && proj.Builtin != 0 {
			if topic, _ := topicOf(p); topic != 0 {
				return topic
			}
		}
		process = p.ParentID.Int64
	}
	return 0
}

type chatTopicLangSetIn struct {
	Lang string `desc:"Code of the language the owner's latest message is written in, such as ru, en or de"`
}

// chatTopicLang marks the language of the topic a reply answers, the one the owner wrote in last.
func (s *Service) chatTopicLang(ctx context.Context, act registry.Actor, in chatTopicLangSetIn) (any, error) {
	p, err := s.St.Q.GetProcess(ctx, act.ProcessID)
	if err != nil {
		return nil, registry.Invalid("only a reply of the master in a chat topic marks its language")
	}
	topic := s.replyOf(ctx, p)
	if topic == 0 {
		return nil, registry.Invalid("only a reply of the master in a chat topic marks its language")
	}
	lang := strings.TrimSpace(in.Lang)
	if !langCode.MatchString(lang) {
		return nil, registry.Invalid("lang is a code of a language, such as ru, en or de, not %q", in.Lang)
	}
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if _, err := s.ownTopic(ctx, q, act, topic); err != nil {
			return err
		}
		n, err := q.SetTopicLang(ctx, db.SetTopicLangParams{Lang: lang, ID: topic})
		if err == nil && n == 0 {
			err = sql.ErrNoRows
		}
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, registry.NotFound("no topic %d", topic)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"topic": topic, "lang": lang}, nil
}
