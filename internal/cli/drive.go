package cli

import (
	"context"
	"fmt"
	"strconv"

	"github.com/duncanite/go-bosch/homeconnect"
	"github.com/duncanite/go-bosch/internal/terminal"
)

// driving opens the appliance under its profile, runs visit within the
// timeout, and closes it. The commands that say what an appliance is
// doing, and that drive it, speak by names: they want the profile.
func (root *rootCommand) driving(ctx context.Context, visit func(context.Context, *homeconnect.Appliance) error) error {
	read, err := root.described()
	if err != nil {
		return err
	}

	if read == nil {
		return fmt.Errorf("%w: name the appliance's profile with --profile", errUsage)
	}

	if root.Timeout <= 0 {
		return fmt.Errorf("%w: --timeout wants a time above zero", errUsage)
	}

	ctx, cancel := context.WithTimeout(ctx, root.Timeout)
	defer cancel()

	appliance, err := reached(ctx, root, func(address string) (*homeconnect.Appliance, error) {
		return homeconnect.Open(ctx, address, root.key, read)
	})
	if err != nil {
		return err
	}

	defer func() { _ = appliance.Close() }()

	return visit(ctx, appliance)
}

// known is a part of a state as it is printed: what it is, or that the
// appliance has not said.
func known[T any](part *T) string {
	if part == nil {
		return "not said"
	}

	return fmt.Sprint(*part)
}

// programmed is a program as it is printed: its name, or its id when the
// profile names it nowhere, or that there is none.
func programmed(program *homeconnect.Program) string {
	switch {
	case program == nil:
		return "not said"
	case program.UID == 0:
		return "none"
	case program.Name == "":
		return strconv.Itoa(program.UID)
	default:
		return program.Name
	}
}

// Run prints what the appliance is doing.
func (*statusCommand) Run(ctx context.Context, root *rootCommand) error {
	return root.driving(ctx, func(_ context.Context, appliance *homeconnect.Appliance) error {
		state := appliance.State()

		for _, line := range [][2]string{
			{"state", known(state.Operation)},
			{"door", known(state.Door)},
			{"power", known(state.Power)},
			{"selected program", programmed(state.Selected)},
			{"running program", programmed(state.Active)},
			{"time left", known(state.Remaining)},
			{"progress", known(state.Progress)},
			{"remote start allowed", known(state.RemoteStart)},
		} {
			terminal.Printf(root.out, "%s: %s\n", line[0], line[1])
		}

		return nil
	})
}

// Run holds the running program.
func (*pauseCommand) Run(ctx context.Context, root *rootCommand) error {
	return root.driving(ctx, func(ctx context.Context, appliance *homeconnect.Appliance) error {
		return appliance.Pause(ctx)
	})
}

// Run goes on with a paused program.
func (*resumeCommand) Run(ctx context.Context, root *rootCommand) error {
	return root.driving(ctx, func(ctx context.Context, appliance *homeconnect.Appliance) error {
		return appliance.Resume(ctx)
	})
}

// Run ends the running program.
func (*stopCommand) Run(ctx context.Context, root *rootCommand) error {
	return root.driving(ctx, func(ctx context.Context, appliance *homeconnect.Appliance) error {
		return appliance.Stop(ctx)
	})
}

// Run starts the selected program.
func (*startCommand) Run(ctx context.Context, root *rootCommand) error {
	return root.driving(ctx, func(ctx context.Context, appliance *homeconnect.Appliance) error {
		return appliance.Start(ctx)
	})
}

// Run turns the appliance on, or off.
func (cmd *powerCommand) Run(ctx context.Context, root *rootCommand) error {
	return root.driving(ctx, func(ctx context.Context, appliance *homeconnect.Appliance) error {
		return appliance.SetPower(ctx, cmd.State == "on")
	})
}
