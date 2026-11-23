// Package cli is the homeconnect command: the BSH Home Connect appliances
// on the LAN, as they announce themselves, and one of them spoken to over
// its own WebSocket: what it says of itself, the values it holds, what it
// tells as it changes, and any request beside. The wiring of flags and
// output over the homeconnect package, with the Multicast DNS the
// appliances are searched for over, which is the program's and not the
// library's. The appliance's key comes from the environment and from
// nowhere else.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/alecthomas/kong"
	"github.com/forkcloser/go-mdns/transport"

	"github.com/duncanite/go-bosch/homeconnect"
	"github.com/duncanite/go-bosch/homeconnect/account"
	"github.com/duncanite/go-bosch/internal/terminal"
)

// Exit statuses.
const (
	ExitOK    = 0
	ExitError = 1
)

// The environment variables the appliance's key is read from: the key of
// its profile, and the initial vector beside it.
const (
	EnvKey    = "HOMECONNECT_KEY" // #nosec G101 -- the name of an environment variable, not a credential
	EnvVector = "HOMECONNECT_IV"
)

var (
	// errUsage is a command line, or an environment, the command cannot
	// act on.
	errUsage = errors.New("cannot run")
	// errNotFound is an appliance that did not answer a search.
	errNotFound = errors.New("not found")
)

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
	// In is where it reads what is pasted: the address a login ends on.
	In io.Reader
	// Getenv reads the environment, where the appliance's key is.
	Getenv func(string) string
	// Account is where the Home Connect account's API is, and what
	// reaches it; the zero value is BSH's own, in the region the command
	// is told.
	Account account.Options
	// Network is where appliances are searched for; nil is the host's own.
	Network transport.Network
}

type rootCommand struct {
	Host      string        `env:"HOMECONNECT_HOST"      help:"The appliance's address on the network; with none, the appliance is searched for."`
	ID        string        `env:"HOMECONNECT_ID"        help:"The id of the appliance to speak to, as discover prints it, when several announce themselves."`
	Interface string        `env:"HOMECONNECT_INTERFACE" help:"The network interface the appliances are on, when the host has several."`
	Wait      time.Duration `default:"3s"                env:"HOMECONNECT_WAIT"                                                                                                        help:"How long a search waits for appliances to answer."`
	Timeout   time.Duration `default:"15s"               help:"How long to wait for the appliance, its search included, for every command but watch."`
	Profile   string        `env:"HOMECONNECT_PROFILE"   help:"The appliance's profile, the archive account --profiles wrote: with it, values and watch name what the appliance says."`

	Discover discoverCommand `cmd:"" help:"Print the appliances that announce themselves on this network. Needs no key."`
	Account  accountCommand  `cmd:"" help:"Fetch from the Home Connect account what opens each of its appliances: the key and the initial vector. Needs a browser to sign in with."`
	Info     infoCommand     `cmd:"" help:"Print what the appliance says of itself: its brand, its model, its versions."`
	Services servicesCommand `cmd:"" help:"Print the services the appliance has, each with its version."`
	Status   statusCommand   `cmd:"" help:"Print what the appliance is doing: its state, its door, its program and the time left. Needs --profile."`
	Pause    pauseCommand    `cmd:"" help:"Hold the running program. Needs --profile, as every command that drives the appliance."`
	Resume   resumeCommand   `cmd:"" help:"Go on with a paused program."`
	Stop     stopCommand     `cmd:"" help:"End the running program."`
	Start    startCommand    `cmd:"" help:"Start the selected program. The appliance wants remote start allowed at its own panel."`
	Power    powerCommand    `cmd:"" help:"Turn the appliance on, or off."`
	Values   valuesCommand   `cmd:"" help:"Print the values the appliance holds, each under its numeric id."`
	Watch    watchCommand    `cmd:"" help:"Print what the appliance says as it says it, until interrupted."`
	Call     callCommand     `cmd:"" help:"Send any request and print the appliance's answer."`
	Features featuresCommand `cmd:"" help:"Print what the profile says the appliance has: each feature's id, kind, access and name. Needs --profile, and no key."`

	in      io.Reader
	out     io.Writer
	err     io.Writer
	getenv  func(string) string
	network transport.Network
	api     account.Options
	key     homeconnect.Key
}

type (
	discoverCommand struct {
		Whole bool `help:"Print, under each appliance, the host it names and every entry of what it announces."`
	}
	accountCommand struct {
		Region   string `env:"HOMECONNECT_REGION"                                                                      help:"Where the account is kept: eu, na or cn."`
		Profiles string `help:"A directory to write each appliance's profile into, as <id>.zip: what --profile reads."`
	}
	featuresCommand struct{}
	statusCommand   struct{}
	pauseCommand    struct{}
	resumeCommand   struct{}
	stopCommand     struct{}
	startCommand    struct{}
	powerCommand    struct {
		State string `arg:"" enum:"on,off" help:"on or off."`
	}
	infoCommand struct {
		Whole bool `help:"Print all the appliance said of itself, in JSON: its serial and its hardware address among it."`
	}
	servicesCommand struct{}
	valuesCommand   struct{}
	watchCommand    struct{}
	callCommand     struct {
		Action   string `arg:"" enum:"get,post,notify"                                 help:"get, post or notify."`
		Resource string `arg:"" help:"The resource: /ro/allMandatoryValues, /ci/info."`
		Data     string `arg:"" help:"What the request carries, in JSON."              optional:""`
	}
)

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

	root := rootCommand{
		in: env.In, out: env.Out, err: env.Err, getenv: env.Getenv, network: env.Network, api: env.Account,
	}
	if root.getenv == nil {
		root.getenv = func(string) string { return "" }
	}

	parser, err := kong.New(
		&root,
		kong.Name("homeconnect"),
		kong.Description(
			"Find the BSH Home Connect appliances on the LAN, Bosch, Thermador, Siemens, Gaggenau, and speak to one. "+
				"The appliance's key is read from "+EnvKey+" and its initial vector from "+EnvVector+
				": the two the account command prints, from the owner's Home Connect account.",
		),
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

	// What is on the network is anyone's to hear, the account is asked for
	// the key, and a profile is a file: none of them asks for a key.
	if command := kctx.Command(); command != "discover" && command != "account" && command != "features" {
		err = root.unlock()
	}

	if err == nil {
		err = kctx.Run(&root)
	}

	if err != nil {
		// An error that joins several says each on a line of its own:
		// each is printed as a line of the command's.
		for line := range strings.SplitSeq(err.Error(), "\n") {
			terminal.Printf(env.Err, "homeconnect: %s\n", line)
		}

		return ExitError
	}

	return ExitOK
}

// unlock reads the appliance's key from the environment. What is not set
// is said by its variable's name.
func (root *rootCommand) unlock() error {
	var missing []string

	for _, name := range []string{EnvKey, EnvVector} {
		if root.getenv(name) == "" {
			missing = append(missing, name)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("%w: not set in the environment: %s", errUsage, strings.Join(missing, ", "))
	}

	key, err := homeconnect.ParseKey(root.getenv(EnvKey), root.getenv(EnvVector))
	if err != nil {
		return fmt.Errorf("%s, %s: %w", EnvKey, EnvVector, err)
	}

	root.key = key

	return nil
}

// with opens a session with the appliance, runs visit within the timeout,
// and closes the session.
func (root *rootCommand) with(ctx context.Context, visit func(context.Context, *homeconnect.Session) error) error {
	if root.Timeout <= 0 {
		return fmt.Errorf("%w: --timeout wants a time above zero", errUsage)
	}

	ctx, cancel := context.WithTimeout(ctx, root.Timeout)
	defer cancel()

	session, err := root.dial(ctx)
	if err != nil {
		return err
	}

	defer func() { _ = session.Close() }()

	return visit(ctx, session)
}
