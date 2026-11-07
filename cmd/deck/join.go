package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	deckhome "deck2/internal/home"
	"deck2/internal/node"
	"deck2/internal/provider/remote"
	"deck2/internal/release"
	"deck2/internal/sandbox"
)

// join is deck -join: it pairs this machine with deck as a node by a code of deck pair --node, installs or removes
// the service that keeps it connected, or serves deck as its node until a signal.
func join(args []string) error {
	var pos []string
	var install, uninstall bool
	name, allow := "", ""
	for i := 0; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "-") {
			pos = append(pos, args[i])
			continue
		}
		switch strings.TrimLeft(args[i], "-") {
		case "install":
			install = true
		case "uninstall":
			uninstall = true
		case "name", "allow":
			if i+1 == len(args) {
				return fmt.Errorf("%s takes a value: --name the name of the node, --allow the ports, agents and folders deck may use, such as 8178,8190,claude-code,~/code/repo", args[i])
			}
			i++
			if strings.TrimLeft(args[i-1], "-") == "name" {
				name = args[i]
			} else {
				allow = args[i]
			}
		default:
			return fmt.Errorf("deck -join knows no flag %s; it takes deck's address, the code of deck pair --node, --install, --uninstall, --name and --allow", args[i])
		}
	}
	h, err := nodeHome()
	if err != nil {
		return err
	}
	if uninstall {
		return uninstallService(h)
	}
	if len(pos) == 0 || len(pos) > 2 {
		return errors.New("deck -join <deck's address> [<code of deck pair --node>] [--install]")
	}
	address, err := node.Address(pos[0])
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	allowed, err := node.ParseAllowed(allow)
	if err != nil {
		return fmt.Errorf("--allow: %w", err)
	}
	var j node.Joined
	if len(pos) == 2 {
		host, _ := os.Hostname()
		if j, err = node.Pair(ctx, address, pos[1], cmp.Or(name, strings.TrimSuffix(host, ".local"))); err != nil {
			return err
		}
		// The list comes before the key, so a node that has its key has its list too.
		if allow != "" {
			if err := node.SaveAllowed(h, allowed); err != nil {
				return err
			}
		}
		if err := node.Save(h, j); err != nil {
			return err
		}
		fmt.Printf("This machine is the node %d of deck at %s; its key is in %s.\n", j.Device, address, h.NodeKey())
	} else if j, err = node.Load(h); err != nil {
		return err
	}
	if j.Deck != address {
		return fmt.Errorf("this machine is the node of deck at %s, not %s: pair it with a code of that deck, deck -join %s <code>", j.Deck, address, address)
	}
	if allow != "" && len(pos) < 2 {
		if err := node.SaveAllowed(h, allowed); err != nil {
			return err
		}
	}
	if a := node.ReadAllowed(h); a.Empty() {
		fmt.Printf("Deck may use nothing on this machine yet: name the ports of its models, the agents and the folders with --allow, such as deck -join %s --install --allow 8178,claude-code,~/code/repo, or in %s.\n", address, h.NodeAllowed())
	} else {
		fmt.Printf("Deck may use on this machine: %s (%s).\n", a, h.NodeAllowed())
	}
	if install {
		return installService(ctx, h, address)
	}
	lock, err := lockNode(h)
	if err != nil {
		return err
	}
	defer lock.Close()
	j.Version = release.Version
	back, err := node.ServeBack(j)
	if err != nil {
		return err
	}
	defer back.Close()
	sb := sandbox.New(h)
	sb.Reap()
	serving := node.Serving{Allowed: func() node.Allowed { return node.ReadAllowed(h) }, Own: ownFolders(h)}
	agents := &remote.Server{Home: h, Sandbox: sb, Back: back.URL, Allowed: serving.Allowed, Folder: serving.Folder}
	agents.Sweep()
	serving.Agents, serving.Has = agents, agents.Has
	node.Run(ctx, j, serving)
	return nil
}

// nodeHome is the folder of the node of this machine: DECK_HOME, else .deck2-node in the user's home, apart from the
// folder and the lock of a deck there. A key of a node in deck's own folder, as nodes kept it before, moves there.
func nodeHome() (deckhome.Layout, error) {
	u, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	old := deckhome.Layout(filepath.Join(u, ".deck2"))
	if h := os.Getenv("DECK_HOME"); h != "" {
		if filepath.Clean(h) == string(old) {
			if _, err := os.Stat(old.NodeKey()); err == nil {
				fmt.Printf("This node keeps its key in %s, the folder of deck: run deck -join <deck's address> --install once without DECK_HOME, and it moves to %s.\n", old, filepath.Join(u, ".deck2-node"))
			}
		}
		return deckhome.Layout(h), nil
	}
	h := deckhome.Layout(filepath.Join(u, ".deck2-node"))
	if moved, err := node.Move(old, h); err != nil {
		return "", err
	} else if moved {
		fmt.Printf("The key of this node moved from %s to %s: a node keeps a folder of its own, apart from deck's.\n", old.Node(), h.Node())
	}
	return h, nil
}

// ownFolders are the folders of deck on this machine, which no folder of a project holds or is in: the node's own and
// those of a deck and a node at their places.
func ownFolders(h deckhome.Layout) []string {
	out := []string{string(h)}
	if u, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(u, ".deck2"), filepath.Join(u, ".deck2-node"))
	}
	return out
}

// lockNode holds the lock of the node of a home until it exits; one home serves one node.
func lockNode(h deckhome.Layout) (*os.File, error) {
	if err := os.MkdirAll(h.Node(), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(h.Node(), "node.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("the node of %s runs already; its service keeps it: deck -join --uninstall stops it", string(h))
	}
	return f, nil
}
