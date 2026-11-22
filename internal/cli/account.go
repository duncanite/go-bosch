package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/duncanite/go-bosch/homeconnect/account"
	"github.com/duncanite/go-bosch/internal/terminal"
)

// pasted bounds the address a login ends on, as it is pasted.
const pasted = 8192

// Run signs in to the Home Connect account, with the owner's browser,
// and prints each of its appliances with what opens it. What it prints
// last on each line is a secret.
func (cmd *accountCommand) Run(ctx context.Context, root *rootCommand) error {
	if root.Timeout <= 0 {
		return fmt.Errorf("%w: --timeout wants a time above zero", errUsage)
	}

	options := root.api
	if cmd.Region != "" {
		options.Region = account.Region(cmd.Region)
	}

	if options.Region == "" && (options.Login == "" || options.Assets == "") {
		return fmt.Errorf("%w: say where the account is kept with --region: eu, na or cn", errUsage)
	}

	client, err := account.New(options)
	if err != nil {
		return err
	}

	login, err := client.Begin()
	if err != nil {
		return err
	}

	terminal.Printf(root.err, "Open this address in a browser, with its developer tools open on the network tab,\n"+
		"and sign in to Home Connect:\n\n  %s\n\n"+
		"Signing in ends on an address that begins with hcauth://, which the browser cannot open.\n"+
		"It is the Location header of the answer to the last request, named redirect_target.\n"+
		"Copy that address, paste it here and press Enter:\n", login.URL)

	ended, err := line(ctx, root.in)
	if err != nil {
		return err
	}

	// The wait for a person is not timed; the account's answers are.
	ctx, cancel := context.WithTimeout(ctx, root.Timeout)
	defer cancel()

	session, err := client.Finish(ctx, login, ended)
	if err != nil {
		return err
	}

	// An appliance whose key was not given is an error said after the
	// others are printed.
	appliances, unread := client.Appliances(ctx, session)
	if unread != nil && !errors.Is(unread, account.ErrUnread) {
		return unread
	}

	if len(appliances) == 0 && unread == nil {
		terminal.Printf(root.out, "The account has no appliance.\n")

		return nil
	}

	table := tabwriter.NewWriter(root.out, 0, 0, 2, ' ', 0)

	terminal.Printf(table, "ID\tBRAND\tTYPE\tMODEL\tMODE\tKEY\tIV\n")

	for _, appliance := range appliances {
		terminal.Printf(table, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", appliance.ID, shown(appliance.Brand),
			shown(appliance.Type), shown(appliance.Model), appliance.Mode, appliance.Key, shown(appliance.Vector))
	}

	if err = table.Flush(); err != nil {
		return fmt.Errorf("writing the appliances: %w", err)
	}

	return unread
}

// line reads one line of what is typed or pasted, within ctx and within
// a bound: what is read is a stranger's until the account takes it.
func line(ctx context.Context, typed io.Reader) (string, error) {
	if typed == nil {
		return "", fmt.Errorf("%w: nothing to read the pasted address from", errUsage)
	}

	type read struct {
		text string
		err  error
	}

	done := make(chan read, 1)

	go func() {
		text, err := bufio.NewReader(io.LimitReader(typed, pasted+1)).ReadString('\n')
		done <- read{text, err}
	}()

	select {
	case <-ctx.Done():
		return "", fmt.Errorf("waiting for the address to be pasted: %w", context.Cause(ctx))
	case got := <-done:
		switch {
		case len(got.text) > pasted:
			return "", fmt.Errorf("%w: what was pasted is longer than %d bytes", errUsage, pasted)
		case got.err != nil && got.text == "":
			return "", fmt.Errorf("%w: no address was pasted", errUsage)
		}

		return got.text, nil
	}
}
