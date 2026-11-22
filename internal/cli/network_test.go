package cli_test

import (
	"bytes"
	"net/netip"
	"strings"
	"testing"

	mdns "github.com/forkcloser/go-mdns"
	"github.com/forkcloser/go-mdns/mdnstest"
	"github.com/forkcloser/go-mdns/transport"

	"github.com/duncanite/go-bosch/internal/cli"
)

// wire is the one wire of the test networks.
const wire = "home"

// announcing puts something on the network that announces a Home Connect
// service, on behalf of a host of the name and at the address given, with
// the TXT record given.
func announcing(t *testing.T, network *mdnstest.Network, host, at string, text mdns.TXT) {
	t.Helper()

	address := netip.MustParseAddrPort(at)

	peer, err := mdns.Open(t.Context(), mdns.Config{
		Network: network.Host(mdnstest.Port{Interface: "wlan0", Wire: wire}),
	})
	if err != nil {
		t.Fatalf("opening the network of %s: %v", host, err)
	}

	t.Cleanup(func() { _ = peer.Close() })

	if _, err = peer.Announce(t.Context(), mdns.Service{
		Name: host, Type: "_homeconnect._tcp", Port: address.Port(), Text: text,
		Host: host, Addrs: []netip.Addr{address.Addr()},
	}); err != nil {
		t.Fatalf("announcing %s: %v", host, err)
	}
}

// entries is a TXT record of keys and values, in pairs.
func entries(pairs ...string) mdns.TXT {
	text := make(mdns.TXT, 0, len(pairs)/2)
	for index := 0; index+1 < len(pairs); index += 2 {
		text = append(text, mdns.Entry{Key: pairs[index], Value: []byte(pairs[index+1])})
	}

	return text
}

// here is the command's own host on the network: on the appliances' wire
// by en0, and by en1 on one where nothing is.
func here(network *mdnstest.Network) transport.Network {
	return network.Host(
		mdnstest.Port{Interface: "en0", Wire: wire},
		mdnstest.Port{Interface: "en1", Wire: "elsewhere"},
	)
}

// run is the command on a network; its output and what it wrote of
// errors, then its status.
func run(t *testing.T, network transport.Network, args ...string) (out, errs string, status int) {
	t.Helper()

	var stdout, stderr bytes.Buffer

	status = cli.Run(t.Context(), cli.Environment{
		Args: args, Out: &stdout, Err: &stderr, Network: network,
		Getenv: func(name string) string { return key()[name] },
	})

	return stdout.String(), stderr.String(), status
}

// rows is the output as lines of fields, the spacing of a table aside.
func rows(out string) string {
	var lines []string

	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		lines = append(lines, strings.Join(strings.Fields(line), " "))
	}

	return strings.Join(lines, "\n")
}

// TestDiscover: the appliances that announce themselves are printed, each
// with who it says it is, what it says it is and where, with the port it
// announces, in the order of their ids; what it does not say is a dash,
// and what announces the service with no id is not an appliance. Whole,
// each is printed with its host and every entry of its TXT record. A
// network with none says so, as does an interface nothing is on; a wait
// of nothing is refused.
func TestDiscover(t *testing.T) {
	t.Parallel()

	var network mdnstest.Network

	announcing(
		t,
		&network,
		"THERMADOR-Dishwasher-000000000002",
		"192.0.2.20:443",
		entries(
			"id",
			"THERMADOR-DWHD000XXX-000000000002",
			"brand",
			"THERMADOR",
			"type",
			"Dishwasher",
			"vib",
			"DWHD000XXX",
		),
	)
	announcing(
		t,
		&network,
		"000000000001",
		"192.0.2.10:80",
		entries("id", "BOSCH-WAX000XXX-000000000001", "extra", "kept"),
	)
	announcing(t, &network, "something-else", "192.0.2.30:80", entries("brand", "NONE"))

	out, errs, status := run(t, here(&network), "--wait", "2s", "discover")
	if want := strings.Join([]string{
		"ID BRAND TYPE MODEL AT",
		"BOSCH-WAX000XXX-000000000001 - - - 192.0.2.10:80",
		"THERMADOR-DWHD000XXX-000000000002 THERMADOR Dishwasher DWHD000XXX 192.0.2.20:443",
	}, "\n"); status != cli.ExitOK || errs != "" || rows(out) != want {
		t.Errorf("discover exits %d, saying %q and\n%s\nwant\n%s", status, errs, rows(out), want)
	}

	out, errs, status = run(t, here(&network), "--wait", "2s", "discover", "--whole")
	if want := strings.Join([]string{
		"BOSCH-WAX000XXX-000000000001",
		"  host: 000000000001.local",
		"  at: 192.0.2.10:80",
		"  extra=kept",
		"  id=BOSCH-WAX000XXX-000000000001",
		"THERMADOR-DWHD000XXX-000000000002",
		"  host: THERMADOR-Dishwasher-000000000002.local",
		"  at: 192.0.2.20:443",
		"  brand=THERMADOR",
		"  id=THERMADOR-DWHD000XXX-000000000002",
		"  type=Dishwasher",
		"  vib=DWHD000XXX",
		"",
	}, "\n"); status != cli.ExitOK || errs != "" || out != want {
		t.Errorf("discover --whole exits %d, saying %q and\n%s\nwant\n%s", status, errs, out, want)
	}

	out, errs, status = run(t, here(&network), "--wait", "500ms", "--interface", "en1", "discover")
	if status != cli.ExitOK || errs != "" || out != "No appliance answered.\n" {
		t.Errorf("discover where nothing is exits %d, saying %q and %q", status, errs, out)
	}

	out, errs, status = run(t, here(&network), "--wait", "0s", "discover")
	if status != cli.ExitError || out != "" || !strings.Contains(errs, "--wait wants a time above zero") {
		t.Errorf("discover with no time to wait exits %d, saying %q and %q", status, out, errs)
	}
}

// TestWhatAnAppliancePrints: what an appliance announces is a stranger's
// text. A line's end and a control in it are written out, and forge no
// line of the command's own.
func TestWhatAnAppliancePrints(t *testing.T) {
	t.Parallel()

	var network mdnstest.Network

	announcing(t, &network, "000000000003", "192.0.2.40:80",
		entries("id", "ID\nFORGED 1 2 3", "brand", "B\x1b[2JRAND", "type", "Oven", "vib", "V"))

	for _, args := range [][]string{{"--wait", "2s", "discover"}, {"--wait", "2s", "discover", "--whole"}} {
		out, errs, status := run(t, here(&network), args...)
		if status != cli.ExitOK || errs != "" || strings.Contains(out, "\x1b") ||
			strings.Contains(out, "\nFORGED") || !strings.Contains(out, `ID\nFORGED 1 2 3`) {
			t.Errorf("%v exits %d, saying %q and %q", args, status, errs, out)
		}
	}
}

// TestUsage: --help prints the commands and exits with nothing wrong; a
// command there is not is refused.
func TestUsage(t *testing.T) {
	t.Parallel()

	var network mdnstest.Network

	out, _, status := run(t, here(&network), "--help")
	if status != cli.ExitOK || !strings.Contains(out, "discover") {
		t.Errorf("--help exits %d, saying %q", status, out)
	}

	_, errs, status := run(t, here(&network), "launch")
	if status != cli.ExitError || errs == "" {
		t.Errorf("a command there is not exits %d, saying %q", status, errs)
	}
}
