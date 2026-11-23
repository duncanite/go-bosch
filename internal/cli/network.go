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
// gives them in the order of their ids. It ends sooner once one is
// enough.
func (root *rootCommand) search(ctx context.Context, enough func(homeconnect.Found) bool) ([]homeconnect.Found, error) {
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
	sated := false

	err = homeconnect.Find(searching, lent{peer: peer}, func(appliance homeconnect.Found, lost bool) {
		if lost {
			delete(found, appliance.ID)

			return
		}

		found[appliance.ID] = appliance

		if enough != nil && enough(appliance) {
			sated = true

			cancel()
		}
	})

	if ctx.Err() != nil {
		return nil, fmt.Errorf("searching for appliances: %w", context.Cause(ctx))
	}

	if !sated && !errors.Is(err, context.DeadlineExceeded) {
		return nil, fmt.Errorf("searching for appliances: %w", err)
	}

	appliances := make([]homeconnect.Found, 0, len(found))
	for _, id := range slices.Sorted(maps.Keys(found)) {
		appliances = append(appliances, found[id])
	}

	return appliances, nil
}

// places is where to reach the appliance: --host, or with none the
// addresses of the appliance found on the network, the one --id names or
// the only one there is. Among several with none named it chooses none:
// the key is one appliance's, and a key tried at another is refused.
func (root *rootCommand) places(ctx context.Context) ([]string, error) {
	if root.Host != "" {
		return []string{root.Host}, nil
	}

	var enough func(homeconnect.Found) bool

	if root.ID != "" {
		enough = func(appliance homeconnect.Found) bool {
			return appliance.ID == root.ID && len(appliance.Addresses) > 0
		}
	}

	appliances, err := root.search(ctx, enough)
	if err != nil {
		return nil, err
	}

	chosen, err := root.choose(appliances)
	if err != nil {
		return nil, err
	}

	if len(chosen.Addresses) == 0 {
		return nil, fmt.Errorf(
			"%w: the appliance %s announces no address; name one with --host",
			errNotFound,
			chosen.ID,
		)
	}

	addresses := make([]string, 0, len(chosen.Addresses))
	for _, address := range chosen.Addresses {
		addresses = append(addresses, address.String())
	}

	return addresses, nil
}

// reached is what opening the appliance gives, at the first of the places
// it may be at that opens.
func reached[T any](ctx context.Context, root *rootCommand, open func(address string) (*T, error)) (*T, error) {
	addresses, err := root.places(ctx)
	if err != nil {
		return nil, err
	}

	failure := fmt.Errorf("%w: the appliance has no address to be reached at", errNotFound)

	for _, address := range addresses {
		opened, openErr := open(address)
		if openErr == nil && opened != nil {
			return opened, nil
		}

		if openErr != nil {
			failure = openErr
		}

		// A key one address refused is refused at them all.
		if errors.Is(openErr, homeconnect.ErrKey) {
			break
		}
	}

	return nil, failure
}

// dial opens a session with the appliance.
func (root *rootCommand) dial(ctx context.Context) (*homeconnect.Session, error) {
	return reached(ctx, root, func(address string) (*homeconnect.Session, error) {
		return homeconnect.Dial(ctx, address, root.key)
	})
}

// choose is the appliance to speak to, of those found.
func (root *rootCommand) choose(appliances []homeconnect.Found) (homeconnect.Found, error) {
	if root.ID != "" {
		for _, appliance := range appliances {
			if appliance.ID == root.ID {
				return appliance, nil
			}
		}

		return homeconnect.Found{}, fmt.Errorf(
			"%w: the appliance %s did not answer the search within %s; name its address with --host",
			errNotFound, root.ID, root.Wait,
		)
	}

	switch len(appliances) {
	case 0:
		return homeconnect.Found{}, fmt.Errorf(
			"%w: no appliance answered the search within %s; name an address with --host", errNotFound, root.Wait,
		)
	case 1:
		return appliances[0], nil
	default:
		ids := make([]string, 0, len(appliances))
		for _, appliance := range appliances {
			ids = append(ids, appliance.ID)
		}

		return homeconnect.Found{}, fmt.Errorf("%w: %d appliances answered the search, %s; name one with --id",
			errUsage, len(appliances), strings.Join(ids, ", "))
	}
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
	appliances, err := root.search(ctx, nil)
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
