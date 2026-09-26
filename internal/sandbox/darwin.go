//go:build darwin

package sandbox

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"slices"
	"strings"
)

type seatbelt struct{}

func platform() Sandbox { return seatbelt{} }

func (seatbelt) Name() string { return "sandbox-exec" }

func (seatbelt) Check() error {
	_, err := exec.LookPath("sandbox-exec")
	return err
}

func (seatbelt) Listen(string) (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") }

func (seatbelt) Addr(ln net.Listener) string { return ln.Addr().String() }

func (seatbelt) Command(ctx context.Context, p Policy) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", append([]string{"-p", profile(p), "--"}, p.Argv...)...)
	cmd.Dir, cmd.Env = p.Dir, p.Env
	return group(cmd), nil
}

// services are the system services a confined program may look up: user and group names, logging, TLS trust.
var services = []string{"com.apple.system.opendirectoryd.libinfo", "com.apple.system.notification_center",
	"com.apple.system.logger", "com.apple.logd", "com.apple.diagnosticd", "com.apple.trustd", "com.apple.trustd.agent",
	"com.apple.SecurityServer", "com.apple.system.DirectoryService.libinfo_v1", "com.apple.bsd.dirhelper",
	"com.apple.coreservices.launchservicesd", "com.apple.lsd.mapdb", "com.apple.cfprefsd.daemon", "com.apple.cfprefsd.agent"}

// netServices resolve names and read the network configuration, for installing packages.
var netServices = []string{"com.apple.dnssd.service", "com.apple.SystemConfiguration.configd",
	"com.apple.SystemConfiguration.DNSConfiguration", "com.apple.networkd"}

func profile(p Policy) string {
	var b strings.Builder
	b.WriteString("(version 1)\n(deny default)\n")
	b.WriteString("(allow process-exec process-fork sysctl-read file-read-metadata ipc-posix-shm iokit-open pseudo-tty)\n")
	b.WriteString("(allow signal (target same-sandbox))\n(allow file-read*)\n")
	for _, h := range p.Hide {
		fmt.Fprintf(&b, "(deny file-read* (subpath %s))\n", quote(real(h)))
	}
	for _, r := range p.Read {
		fmt.Fprintf(&b, "(allow file-read* (subpath %s))\n", quote(real(r)))
	}
	for _, w := range p.Write {
		fmt.Fprintf(&b, "(allow file-read* file-write* (subpath %s))\n", quote(real(w)))
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
		b.WriteString("(allow network* system-socket)\n")
	default:
		if p.Out != nil {
			_, port, _ := net.SplitHostPort(p.Out.Addr().String())
			fmt.Fprintf(&b, "(allow system-socket)\n(allow network-outbound (remote ip \"localhost:%s\"))\n", port)
		}
	}
	return b.String()
}

func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
