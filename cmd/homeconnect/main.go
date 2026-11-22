// homeconnect finds BSH Home Connect appliances on the LAN; the command is
// internal/cli.
package main

import (
	"context"
	"os"

	"github.com/mycophonic/primordium/app"
	"github.com/mycophonic/primordium/app/shutdown"

	"github.com/duncanite/go-bosch/internal/cli"
)

func main() {
	app.Main(&app.Options{Name: "homeconnect"}, func(ctx context.Context) error {
		env := cli.Environment{Args: os.Args[1:], Out: os.Stdout, Err: os.Stderr}

		if code := cli.Run(ctx, env); code != cli.ExitOK {
			return shutdown.ExitError(code)
		}

		return nil
	})
}
