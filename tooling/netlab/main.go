//go:build linux

// Command netlab shapes the loopback link with a netem qdisc over netlink.
//
// It replaces `tc qdisc ... netem` for tooling/scripts/ws_netlab.sh, so the
// network lab needs no package install inside the container. It needs NET_ADMIN.
//
//	netlab set -delay 50ms -rate-kbit 8000 -loss 1
//	netlab clear
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// loopbackMTU is the Linux default MTU of lo, restored by clear.
const loopbackMTU = 65536

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: netlab set|clear [flags]")
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "netlab:", err)
		os.Exit(1)
	}
}

func run(cmd string, args []string) error {
	link, err := netlink.LinkByName("lo")
	if err != nil {
		return fmt.Errorf("find lo: %w", err)
	}
	attrs := netlink.QdiscAttrs{LinkIndex: link.Attrs().Index, Handle: netlink.MakeHandle(1, 0), Parent: netlink.HANDLE_ROOT}
	switch cmd {
	case "clear":
		if err := netlink.LinkSetMTU(link, loopbackMTU); err != nil {
			return fmt.Errorf("restore lo mtu: %w", err)
		}
		err := netlink.QdiscDel(netlink.NewNetem(attrs, netlink.NetemQdiscAttrs{}))
		// No root qdisc to delete is already the goal state.
		if err != nil && !errors.Is(err, unix.ENOENT) && !errors.Is(err, unix.EINVAL) {
			return fmt.Errorf("delete netem qdisc: %w", err)
		}
		return nil
	case "set":
		fs := flag.NewFlagSet("set", flag.ContinueOnError)
		delay := fs.Duration("delay", 0, "one-way delay; loopback applies it in both directions")
		rateKbit := fs.Uint64("rate-kbit", 0, "rate limit in kbit/s; 0 is unlimited")
		loss := fs.Float64("loss", 0, "packet loss percent")
		// Loopback MTU is 65536, so TCP's 10-segment initial window holds about 650 KB and
		// slow start never limits a push. An internet path MTU of 1500 gives about 14 KB.
		mtu := fs.Int("mtu", 1500, "link MTU; 1500 matches an internet path")
		if err := fs.Parse(args); err != nil {
			return err
		}
		// A new network namespace starts with lo down.
		if err := netlink.LinkSetUp(link); err != nil {
			return fmt.Errorf("set lo up: %w", err)
		}
		if err := netlink.LinkSetMTU(link, *mtu); err != nil {
			return fmt.Errorf("set lo mtu: %w", err)
		}
		if applied, err := netlink.LinkByName("lo"); err != nil || applied.Attrs().MTU != *mtu {
			return fmt.Errorf("lo mtu did not apply: want %d, err %v", *mtu, err)
		}
		netem := netlink.NewNetem(attrs, netlink.NetemQdiscAttrs{
			Latency: uint32(*delay / time.Microsecond),
			Loss:    float32(*loss),
			Rate64:  *rateKbit * 1000 / 8,
			// A rate-limited link queues a large push; the default 1000 packets is enough
			// for 275 KB at 1500-byte segments, but a higher limit avoids tail drops.
			Limit: 10000,
		})
		if err := netlink.QdiscReplace(netem); err != nil {
			return fmt.Errorf("replace netem qdisc: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
}
