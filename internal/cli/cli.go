// Package cli is the homeconnect command: the BSH Home Connect appliances
// on the LAN, as they announce themselves. The wiring of flags and output
// over the homeconnect package, with the Multicast DNS the appliances are
// searched for over, which is the program's and not the library's.
package cli

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/alecthomas/kong"
	"github.com/forkcloser/go-mdns/transport"

	"github.com/duncanite/go-bosch/internal/terminal"
)

// Exit statuses.
const (
	ExitOK    = 0
	ExitError = 1
)

// errUsage is a command line the command cannot act on.
var errUsage = errors.New("cannot run")

// exitStatus is how the parser's own exit, on --help, leaves Run: kong
// calls its exit function and goes on, so the function panics with the
// status and Run recovers it.
type exitStatus int

// Environment is what the command runs in.
type Environment struct {
	// Args is its arguments, without its name.
	Args []string
	// Out and Err are where it writes.
	Out io.Writer
	Err io.Writer
	// Network is where appliances are searched for; nil is the host's own.
	Network transport.Network
}

type rootCommand struct {
	Interface string        `env:"HOMECONNECT_INTERFACE" help:"The network interface the appliances are on, when the host has several."`
	Wait      time.Duration `default:"3s"                env:"HOMECONNECT_WAIT"                                                         help:"How long a search waits for appliances to answer."`

	Discover discoverCommand `cmd:"" help:"Print the appliances that announce themselves on this network."`

	out     io.Writer
	network transport.Network
}

type discoverCommand struct {
	Whole bool `help:"Print, under each appliance, the host it names and every entry of what it announces."`
}

// Run parses the arguments and runs the command.
func Run(ctx context.Context, env Environment) (status int) {
	defer func() {
		if recovered := recover(); recovered != nil {
			exited, ok := recovered.(exitStatus)
			if !ok {
				panic(recovered)
			}

			status = int(exited)
		}
	}()

	root := rootCommand{out: env.Out, network: env.Network}

	parser, err := kong.New(
		&root,
		kong.Name("homeconnect"),
		kong.Description("Find the BSH Home Connect appliances on the LAN: Bosch, Thermador, Siemens, Gaggenau."),
		kong.UsageOnError(),
		kong.Writers(env.Out, env.Err),
		kong.Exit(func(code int) { panic(exitStatus(code)) }),
		kong.BindTo(ctx, (*context.Context)(nil)),
	)
	if err != nil {
		terminal.Printf(env.Err, "homeconnect: %s\n", err)

		return ExitError
	}

	kctx, err := parser.Parse(env.Args)
	if err != nil {
		parser.Errorf("%s", err)

		return ExitError
	}

	if err = kctx.Run(&root); err != nil {
		terminal.Printf(env.Err, "homeconnect: %s\n", err)

		return ExitError
	}

	return ExitOK
}
