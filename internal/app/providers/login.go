package providers

import (
	"context"
	"deck2/internal/words"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"deck2/internal/manifest"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/sandbox"
	"deck2/internal/untrusted"
)

// loginCeiling is how long a sign-in from deck waits for the owner, as long as a device code of Codex lives.
const loginCeiling = 15 * time.Minute

// signIn is a sign-in of a CLI agent from deck: the link its program printed and the code to enter there, or that the
// program waits for the code the page of the link gives.
type signIn struct {
	Provider string `json:"provider"`
	URL      string `json:"url"`
	Code     string `json:"code,omitempty"`
	Paste    bool   `json:"paste"`
	Error    string `json:"error,omitempty"`
	stdin    io.WriteCloser
	stop     context.CancelFunc
}

var (
	ansi     = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
	linkIn   = regexp.MustCompile(`https://\S+`)
	deviceIn = regexp.MustCompile(`\b[A-Z0-9]{4,5}-[A-Z0-9]{4,6}\b`)
)

type agentLoginIn struct {
	Provider string `desc:"A built-in CLI agent: claude-code or codex"`
}

// agentLogin starts the sign-in of a CLI agent and gives its link, once its program printed one.
func (s *Service) agentLogin(ctx context.Context, act registry.Actor, in agentLoginIn) (any, error) {
	si, ok := s.builtinNamed(in.Provider).(provider.SignIn)
	if !ok {
		return nil, registry.Invalid("deck: %s is not a CLI agent deck signs in to", in.Provider)
	}
	l := &signIn{Provider: in.Provider}
	s.mu.Lock()
	if was := s.logins[in.Provider]; was != nil && was.Error == "" {
		v := *was
		s.mu.Unlock()
		if v.URL == "" {
			return nil, registry.Conflict("deck: the sign-in of %s is starting", in.Provider)
		}
		return v, nil
	}
	if s.logins == nil {
		s.logins = map[string]*signIn{}
	}
	s.logins[in.Provider] = l
	s.mu.Unlock()
	started := false
	defer func() {
		if !started {
			s.mu.Lock()
			if s.logins[in.Provider] == l {
				delete(s.logins, in.Provider)
			}
			s.mu.Unlock()
		}
	}()
	cmd, err := si.Login()
	if err != nil {
		return nil, err
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	if cmd.Dir == "" {
		cmd.Dir = os.TempDir()
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout, cmd.Stderr = w, w
	life, stop := context.WithTimeout(s.Ctx, loginCeiling)
	if err := cmd.Start(); err != nil {
		stop()
		out.Close()
		w.Close()
		return nil, err
	}
	started = true
	w.Close()
	s.mu.Lock()
	l.stdin, l.stop = stdin, stop
	s.mu.Unlock()
	shown := make(chan struct{})
	var tail []string
	read := make(chan struct{})
	go func() {
		defer close(read)
		once := false
		var text string
		buf := make([]byte, 4096)
		for {
			n, err := out.Read(buf)
			text += string(buf[:n])
			if len(text) > 16<<10 {
				text = text[len(text)-16<<10:]
			}
			clean := ansi.ReplaceAllString(text, "")
			s.mu.Lock()
			tail = strings.Split(clean, "\n")
			if l.URL == "" {
				l.URL = linkIn.FindString(clean)
			}
			if l.Code == "" {
				l.Code = deviceIn.FindString(linkIn.ReplaceAllString(clean, ""))
			}
			l.Paste = l.Paste || strings.Contains(strings.ToLower(clean), "paste code")
			ready := l.URL != "" && (l.Code != "" || l.Paste)
			s.mu.Unlock()
			if ready && !once {
				once = true
				close(shown)
				s.Changed("health")
			}
			if err != nil {
				break
			}
		}
		if !once {
			close(shown)
		}
	}()
	var exited atomic.Bool
	go func() {
		<-life.Done()
		if !exited.Load() {
			_ = sandbox.Kill(cmd, syscall.SIGKILL)
		}
	}()
	s.Go(func(context.Context) {
		<-read
		err := cmd.Wait()
		exited.Store(true)
		out.Close()
		stdin.Close()
		stop()
		s.mu.Lock()
		if err == nil {
			if s.logins[in.Provider] == l {
				delete(s.logins, in.Provider)
			}
		} else {
			l.Error = fmt.Sprintf("%s sign-in ended: %s", in.Provider, untrusted.Line(err.Error()+": "+strings.Join(lastLines(tail, 3), " "), 600))
		}
		s.mu.Unlock()
		s.Changed("health")
		if err == nil {
			s.startCheck(in.Provider, true)
		}
	})
	select {
	case <-shown:
	case <-time.After(30 * time.Second):
	case <-ctx.Done():
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if l.URL == "" {
		return nil, fmt.Errorf("deck: %s printed no sign-in link: %s", in.Provider, strings.Join(lastLines(tail, 3), " "))
	}
	return *l, nil
}

type agentLoginCodeIn struct {
	Provider string `desc:"The CLI agent being signed in"`
	Code     string `desc:"The code the page of the sign-in link gave"`
}

// agentLoginCode hands the program of a sign-in the code its page gave.
func (s *Service) agentLoginCode(_ context.Context, _ registry.Actor, in agentLoginCodeIn) (any, error) {
	s.mu.Lock()
	var stdin io.WriteCloser
	if l := s.logins[in.Provider]; l != nil && l.Error == "" && l.Paste {
		stdin = l.stdin
	}
	s.mu.Unlock()
	if stdin == nil {
		return nil, registry.Invalid("deck: no sign-in of %s waits for a code; start it again", in.Provider)
	}
	code := strings.TrimSpace(in.Code)
	if code == "" || strings.ContainsAny(code, "\r\n") {
		return nil, registry.Invalid("deck: the code is one line")
	}
	if _, err := io.WriteString(stdin, code+"\n"); err != nil {
		return nil, err
	}
	return map[string]any{"provider": in.Provider}, nil
}

type agentLoginsIn struct {
	Lang string `opt:"" desc:"Language of the failures, such as ru; English without one"`
}

// agentLogins are the sign-ins from deck that wait for the owner or ended in a failure.
func (s *Service) agentLogins(_ context.Context, _ registry.Actor, in agentLoginsIn) ([]signIn, error) {
	ws := words.In(in.Lang)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []signIn{}
	for _, l := range s.logins {
		if l.URL != "" || l.Error != "" {
			v := *l
			v.Error = manifest.TrText(ws, v.Error)
			out = append(out, v)
		}
	}
	slices.SortFunc(out, func(a, b signIn) int { return strings.Compare(a.Provider, b.Provider) })
	return out, nil
}

func lastLines(lines []string, n int) []string {
	var out []string
	for _, l := range lines {
		if l != "" {
			out = append(out, l)
		}
	}
	return out[max(0, len(out)-n):]
}
