package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/duncanite/go-bosch/homeconnect"
	"github.com/duncanite/go-bosch/internal/terminal"
)

// The resource an appliance gives all the values it holds at.
const resourceValues = "/ro/allMandatoryValues"

// shownOfItself is what info prints of an appliance, in this order, by
// the keys the appliance writes them under: what names its make and its
// versions, and nothing that names the one appliance.
//
//nolint:gochecknoglobals // a table, read only
var shownOfItself = []string{
	"brand", "vib", "eNumber", "deviceInfo", "deviceType", "haVersion", "swVersion", "hwVersion",
}

// Run prints what the appliance said of itself in the handshake.
func (cmd *infoCommand) Run(ctx context.Context, root *rootCommand) error {
	return root.with(ctx, func(_ context.Context, session *homeconnect.Session) error {
		itself := session.Identity()
		if itself == nil {
			terminal.Printf(root.out, "The appliance said nothing of itself.\n")

			return nil
		}

		if cmd.Whole {
			root.written("", itself, true)

			return nil
		}

		var fields map[string]json.RawMessage
		if err := json.Unmarshal(itself, &fields); err != nil {
			return fmt.Errorf("what the appliance said of itself: %w", err)
		}

		for _, key := range shownOfItself {
			value, said := fields[key]
			if !said {
				continue
			}

			terminal.Printf(root.out, "%s: %s\n", key, plainly(value))
		}

		return nil
	})
}

// plainly is a JSON value as a person reads it: a string without its
// quotes, anything else as it is written.
func plainly(value json.RawMessage) string {
	var text string
	if json.Unmarshal(value, &text) == nil {
		return text
	}

	return string(value)
}

// Run prints the services the appliance has.
func (*servicesCommand) Run(ctx context.Context, root *rootCommand) error {
	return root.with(ctx, func(_ context.Context, session *homeconnect.Session) error {
		services := session.Services()
		table := tabwriter.NewWriter(root.out, 0, 0, 2, ' ', 0)

		terminal.Printf(table, "SERVICE\tVERSION\n")

		for _, service := range slices.Sorted(maps.Keys(services)) {
			terminal.Printf(table, "%s\t%d\n", service, services[service])
		}

		if err := table.Flush(); err != nil {
			return fmt.Errorf("writing the services: %w", err)
		}

		return nil
	})
}

// Run prints the values the appliance holds: each under its numeric id,
// as the appliance wrote it. What the ids mean is its profile's to say.
func (*valuesCommand) Run(ctx context.Context, root *rootCommand) error {
	return root.with(ctx, func(ctx context.Context, session *homeconnect.Session) error {
		answer, err := session.Get(ctx, resourceValues, nil)
		if err != nil {
			return err
		}

		held, listed := holdings(answer.Data)
		if !listed {
			// An answer of another shape is shown as it came.
			root.written("", answer.Data, true)

			return nil
		}

		table := tabwriter.NewWriter(root.out, 0, 0, 2, ' ', 0)

		terminal.Printf(table, "UID\tVALUE\n")

		for _, one := range held {
			terminal.Printf(table, "%s\t%s\n", one.UID.String(), oneLine(one.Value))
		}

		if err = table.Flush(); err != nil {
			return fmt.Errorf("writing the values: %w", err)
		}

		return nil
	})
}

// holding is one value an appliance holds, under its numeric id.
type holding struct {
	UID   *json.Number    `json:"uid"`
	Value json.RawMessage `json:"value"`
}

// holdings reads an answer as the values an appliance holds: a list in
// which each has its id. It says whether the answer is one.
func holdings(data json.RawMessage) ([]holding, bool) {
	var held []holding

	if json.Unmarshal(data, &held) != nil {
		return nil, false
	}

	for _, one := range held {
		if one.UID == nil {
			return nil, false
		}
	}

	return held, true
}

// oneLine is a JSON value on one line, with nothing a terminal obeys; a
// value that is none is a dash.
func oneLine(value json.RawMessage) string {
	if len(value) == 0 {
		return "-"
	}

	var laid strings.Builder

	root := rootCommand{out: &laid}
	root.written("", value, false)

	return strings.TrimSuffix(laid.String(), "\n")
}

// Run prints each thing the appliance says as it says it, on one line:
// the time, what it is about, and what it carries. It ends when the
// command is interrupted, which is no failure, or when the session does.
func (*watchCommand) Run(ctx context.Context, root *rootCommand) error {
	// The search and the handshake are within the timeout; the watch is
	// not.
	opening, cancel := context.WithTimeout(ctx, root.Timeout)
	session, err := root.dial(opening)

	cancel()

	if err != nil {
		return err
	}

	defer func() { _ = session.Close() }()

	for {
		message, err := session.Next(ctx)

		var unread *homeconnect.UnreadError

		switch {
		case ctx.Err() != nil:
			return nil
		case errors.As(err, &unread):
			root.written(time.Now().Format(time.TimeOnly)+"  unread  ", unread.Message, false)
		case err != nil:
			return err
		default:
			lead := fmt.Sprintf(
				"%s  %s %s  ",
				time.Now().Format(time.TimeOnly),
				message.Action,
				terminal.Text(message.Resource),
			)

			if message.Data == nil {
				terminal.Printf(root.out, "%s\n", strings.TrimRight(lead, " "))
			} else {
				root.written(lead, message.Data, false)
			}
		}
	}
}

// Run sends one request and prints the appliance's answer: what it
// carries, as JSON a person reads. A refusal is the error, with the
// appliance's code.
func (cmd *callCommand) Run(ctx context.Context, root *rootCommand) error {
	var data any

	if cmd.Data != "" {
		if !json.Valid([]byte(cmd.Data)) {
			return fmt.Errorf("%w: the data is not JSON", errUsage)
		}

		data = json.RawMessage(cmd.Data)
	}

	return root.with(ctx, func(ctx context.Context, session *homeconnect.Session) error {
		var (
			answer homeconnect.Message
			err    error
		)

		switch cmd.Action {
		case "get":
			answer, err = session.Get(ctx, cmd.Resource, data)
		case "post":
			answer, err = session.Post(ctx, cmd.Resource, data)
		default:
			return session.Notify(ctx, cmd.Resource, data)
		}

		if err != nil {
			return err
		}

		root.written("", answer.Data, true)

		return nil
	})
}
