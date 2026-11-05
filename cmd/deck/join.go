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
	"deck2/internal/release"
)

// join is deck -join: it pairs this machine with deck as a node by a code of deck pair --node, installs or removes
// the service that keeps it connected, or serves deck as its node until a signal.
func join(args []string) error {
	var pos []string
	var install, uninstall bool
	name := ""
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
		case "name":
			if i+1 == len(args) {
				return errors.New("-name takes the name of the node")
			}
			i++
			name = args[i]
		default:
			return fmt.Errorf("deck -join knows no flag %s; it takes deck's address, the code of deck pair --node, --install, --uninstall and --name", args[i])
		}
	}
	h, err := homeDir()
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
	var j node.Joined
	if len(pos) == 2 {
		host, _ := os.Hostname()
		if j, err = node.Pair(ctx, address, pos[1], cmp.Or(name, strings.TrimSuffix(host, ".local"))); err != nil {
			return err
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
	if install {
		return installService(ctx, h, address)
	}
	lock, err := lockNode(h)
	if err != nil {
		return err
	}
	defer lock.Close()
	j.Version = release.Version
	node.Run(ctx, j)
	return nil
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
