package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"strings"
	"text/tabwriter"

	mdns "github.com/forkcloser/go-mdns"

	"github.com/duncanite/go-bosch/homeconnect"
	"github.com/duncanite/go-bosch/internal/terminal"
)

// lent is the host's mDNS as the library asks for it.
type lent struct {
	peer *mdns.Peer
}

func (l lent) Browse(ctx context.Context, service string, handle func(homeconnect.Announcement, bool)) error {
	//nolint:wrapcheck // the Peer's own word, which the library hands on as it is
	return l.peer.Browse(ctx, mdns.Query{Type: service}, func(change mdns.Change, heard mdns.Instance) {
		announcement := homeconnect.Announcement{
			Host:      heard.Host,
			Text:      make(map[string]string, len(heard.Text)),
			Addresses: make([]netip.AddrPort, 0, len(heard.Addrs)),
		}

		for _, entry := range heard.Text {
			announcement.Text[entry.Key] = string(entry.Value)
		}

		for _, addr := range heard.Addrs {
			announcement.Addresses = append(announcement.Addresses, netip.AddrPortFrom(addr, heard.Port))
		}

		handle(announcement, change == mdns.Lost)
	})
}

// search hears the appliances that announce themselves, for the wait, and
// gives them in the order of their ids.
func (root *rootCommand) search(ctx context.Context) ([]homeconnect.Found, error) {
	if root.Wait <= 0 {
		return nil, fmt.Errorf("%w: --wait wants a time above zero", errUsage)
	}

	cfg := mdns.Config{Network: root.network}

	if root.Interface != "" {
		cfg.Interfaces = mdns.On(root.Interface)
	}

	peer, err := mdns.Open(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("opening the network: %w", err)
	}

	defer func() { _ = peer.Close() }()

	searching, cancel := context.WithTimeout(ctx, root.Wait)
	defer cancel()

	found := make(map[string]homeconnect.Found)

	err = homeconnect.Find(searching, lent{peer: peer}, func(appliance homeconnect.Found, lost bool) {
		if lost {
			delete(found, appliance.ID)

			return
		}

		found[appliance.ID] = appliance
	})

	if ctx.Err() != nil {
		return nil, fmt.Errorf("searching for appliances: %w", context.Cause(ctx))
	}

	if !errors.Is(err, context.DeadlineExceeded) {
		return nil, fmt.Errorf("searching for appliances: %w", err)
	}

	appliances := make([]homeconnect.Found, 0, len(found))
	for _, id := range slices.Sorted(maps.Keys(found)) {
		appliances = append(appliances, found[id])
	}

	return appliances, nil
}

// shown is a value an appliance announced, or a dash where it gave none.
func shown(value string) string {
	if value == "" {
		return "-"
	}

	return value
}

// Run finds the appliances that announce themselves on this network and
// prints each: who it says it is, what it says it is, and where.
func (cmd *discoverCommand) Run(ctx context.Context, root *rootCommand) error {
	appliances, err := root.search(ctx)
	if err != nil {
		return err
	}

	if len(appliances) == 0 {
		terminal.Printf(root.out, "No appliance answered.\n")

		return nil
	}

	if cmd.Whole {
		for _, appliance := range appliances {
			whole(root, appliance)
		}

		return nil
	}

	table := tabwriter.NewWriter(root.out, 0, 0, 2, ' ', 0)

	terminal.Printf(table, "ID\tBRAND\tTYPE\tMODEL\tAT\n")

	for _, appliance := range appliances {
		terminal.Printf(table, "%s\t%s\t%s\t%s\t%s\n",
			appliance.ID, shown(appliance.Brand), shown(appliance.Type), shown(appliance.Model), places(appliance))
	}

	if err = table.Flush(); err != nil {
		return fmt.Errorf("writing the appliances: %w", err)
	}

	return nil
}

// places is where an appliance says it is, as one cell.
func places(appliance homeconnect.Found) string {
	written := make([]string, 0, len(appliance.Addresses))
	for _, address := range appliance.Addresses {
		written = append(written, address.String())
	}

	return shown(strings.Join(written, ", "))
}

// whole prints all an appliance announced: its id, then the host it
// names, where it says it is, and each entry of its TXT record, by key.
func whole(root *rootCommand, appliance homeconnect.Found) {
	terminal.Printf(root.out, "%s\n", appliance.ID)
	terminal.Printf(root.out, "  host: %s\n", shown(appliance.Host))
	terminal.Printf(root.out, "  at: %s\n", places(appliance))

	for _, key := range slices.Sorted(maps.Keys(appliance.Text)) {
		terminal.Printf(root.out, "  %s=%s\n", key, appliance.Text[key])
	}
}
