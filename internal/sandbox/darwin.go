//go:build darwin

package sandbox

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// seatbelt keeps the tags it watches and the log stream that reports them.
type seatbelt struct {
	mu    sync.Mutex
	subs  map[string]func(string)
	end   func()
	ready chan struct{}
	idle  *time.Timer
}

func platform() Sandbox { return &seatbelt{} }

func (*seatbelt) Name() string { return "sandbox-exec" }

func (*seatbelt) Check() error {
	_, err := exec.LookPath("/usr/bin/sandbox-exec")
	return err
}

func (*seatbelt) Listen(string) (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") }

func (*seatbelt) Addr(ln net.Listener) string { return ln.Addr().String() }

func (*seatbelt) Command(ctx context.Context, p Policy) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", append([]string{"-p", profile(p), "--"}, p.Argv...)...)
	cmd.Dir, cmd.Env = p.Dir, p.Env
	return group(cmd), nil
}

// services are the system services a confined program may look up: user and group names, logging, TLS trust.
var services = []string{"com.apple.system.opendirectoryd.libinfo", "com.apple.system.notification_center",
	"com.apple.system.logger", "com.apple.logd", "com.apple.diagnosticd", "com.apple.trustd", "com.apple.trustd.agent",
	"com.apple.SecurityServer", "com.apple.system.DirectoryService.libinfo_v1", "com.apple.bsd.dirhelper",
	"com.apple.coreservices.launchservicesd", "com.apple.lsd.mapdb", "com.apple.cfprefsd.daemon", "com.apple.cfprefsd.agent"}

// netServices resolve names and read the network configuration, for programs with the whole network.
var netServices = []string{"com.apple.dnssd.service", "com.apple.SystemConfiguration.configd",
	"com.apple.SystemConfiguration.DNSConfiguration", "com.apple.networkd"}

// sysctls are what the machine and its system are; the processes on it are not.
const sysctls = `(allow sysctl-read (sysctl-name-prefix "hw.") (sysctl-name-prefix "machdep.") (sysctl-name-prefix "kern.")
  (sysctl-name-prefix "vm.") (sysctl-name-prefix "sysctl.") (sysctl-name-prefix "user.") (sysctl-name-prefix "net.routetable")
  (sysctl-name "security.mac.lockdown_mode_state"))
(deny sysctl-read (sysctl-name-prefix "kern.proc"))
`

func profile(p Policy) string {
	var b strings.Builder
	b.WriteString("(version 1)\n(deny default)\n")
	b.WriteString("(allow process-exec process-fork file-read-metadata ipc-posix-shm iokit-open pseudo-tty)\n")
	b.WriteString(sysctls)
	b.WriteString("(allow signal (target same-sandbox))\n(allow file-read*)\n")
	report := ""
	if p.Tag != "" {
		report = fmt.Sprintf(" (with message %s)", quote(mark+" "+p.Tag))
	}
	for _, h := range p.Hide {
		fmt.Fprintf(&b, "(deny file-read* (subpath %s)%s)\n", quote(real(h)), report)
	}
	for _, r := range p.Read {
		fmt.Fprintf(&b, "(allow file-read* (subpath %s))\n", quote(real(r)))
	}
	for _, h := range p.Private {
		fmt.Fprintf(&b, "(deny file-read* (subpath %s)%s)\n", quote(real(h)), report)
	}
	for _, w := range p.Write {
		fmt.Fprintf(&b, "(allow file-read* file-write* (subpath %s))\n", quote(real(w)))
	}
	open := append(slices.Clone(p.Read), p.Write...)
	for _, d := range ancestors(open) {
		fmt.Fprintf(&b, "(allow file-read-metadata (literal %s))\n", quote(d))
		hidden := slices.ContainsFunc(p.Hide, func(h string) bool { return strings.HasPrefix(d+"/", real(h)+"/") })
		if hidden && !slices.ContainsFunc(open, func(o string) bool { return strings.HasPrefix(d+"/", real(o)+"/") }) {
			fmt.Fprintf(&b, "(deny file-read-data (literal %s) (with no-report))\n", quote(d))
		}
	}
	b.WriteString(`(allow file-write* file-ioctl (literal "/dev/null") (literal "/dev/zero") (literal "/dev/tty") (literal "/dev/dtracehelper"))` + "\n")
	b.WriteString("(allow mach-lookup")
	names := services
	if p.Net {
		names = append(slices.Clone(services), netServices...)
	}
	for _, s := range names {
		fmt.Fprintf(&b, " (global-name %s)", quote(s))
	}
	b.WriteString(")\n")
	switch {
	case p.Net:
		b.WriteString("(allow network* system-socket)\n(deny network-outbound (remote unix-socket))\n(allow network-outbound (remote unix-socket (path-literal \"/private/var/run/mDNSResponder\")))\n")
	default:
		if p.Out != nil {
			_, port, _ := net.SplitHostPort(p.Out.Addr().String())
			fmt.Fprintf(&b, "(allow system-socket)\n(allow network-outbound (remote ip \"localhost:%s\"))\n", port)
		}
		if p.Port != 0 {
			fmt.Fprintf(&b, "(allow system-socket)\n(allow network-bind network-inbound (local ip \"localhost:%d\"))\n", p.Port)
			fmt.Fprintf(&b, "(allow network-outbound (remote ip \"localhost:%d\"))\n", p.Port)
		}
	}
	return b.String()
}

// mark starts the message a refusal of a tagged program carries into the log of the system.
const mark = "deck-sandbox:"

// refused is a read of the contents of a path; a look at whether it exists is not reported.
var refused = regexp.MustCompile(`(?s)deny\(\d+\) file-read-data (.+?)\n` + mark + ` (\S+)`)

func (s *seatbelt) Watch(tag string, f func(string)) (func(), <-chan struct{}) {
	var stopped atomic.Bool
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.subs == nil {
		s.subs = map[string]func(string){}
	}
	s.subs[tag] = func(path string) {
		if !stopped.Load() {
			f(path)
		}
	}
	if s.idle != nil {
		s.idle.Stop()
		s.idle = nil
	}
	if s.end == nil {
		s.start()
	}
	return func() {
		stopped.Store(true)
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.subs, tag)
		if len(s.subs) == 0 && s.end != nil && s.idle == nil {
			s.idle = time.AfterFunc(time.Minute, s.quiet)
		}
	}, s.ready
}

// quiet ends the log stream once no program has been watched for a minute.
func (s *seatbelt) quiet() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.idle = nil
	if len(s.subs) == 0 && s.end != nil {
		s.end()
		s.end = nil
	}
}

// start runs the log stream that reports the refusals of tagged programs; one that ends while programs are
// watched starts again. It runs under s.mu.
func (s *seatbelt) start() {
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	s.end, s.ready = cancel, ready
	// The shell ends the stream once deck's end of its stdin closes, so a stream never outlives deck.
	cmd := group(exec.CommandContext(ctx, "/bin/sh", "-c", `/usr/bin/log stream --style ndjson --predicate "$0" & read _; kill $!`,
		`sender == "Sandbox" AND eventMessage CONTAINS "`+mark+`"`))
	in, err := cmd.StdinPipe()
	var out io.ReadCloser
	if err == nil {
		out, err = cmd.StdoutPipe()
	}
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		close(ready)
		return
	}
	s.end = func() { cancel(); _ = in.Close() }
	go func() {
		started := sync.OnceFunc(func() { close(ready) })
		defer started()
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			started()
			var e struct {
				EventMessage string `json:"eventMessage"`
			}
			if json.Unmarshal(sc.Bytes(), &e) != nil {
				continue
			}
			m := refused.FindStringSubmatch(e.EventMessage)
			if m == nil {
				continue
			}
			s.mu.Lock()
			f := s.subs[m[2]]
			s.mu.Unlock()
			if f != nil {
				f(m[1])
			}
		}
		_ = cmd.Wait()
		if ctx.Err() != nil {
			return
		}
		time.Sleep(time.Second)
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.ready == ready && len(s.subs) > 0 {
			s.start()
		}
	}()
}

// ancestors are the folders above paths: a program resolves the path of a folder it may read, and a look into a
// hidden one, as a bundler that walks up for its packages does, stays closed without a report.
func ancestors(paths []string) []string {
	var out []string
	for _, p := range paths {
		for d := filepath.Dir(real(p)); d != "/" && !slices.Contains(out, d); d = filepath.Dir(d) {
			out = append(out, d)
		}
	}
	return out
}

func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
