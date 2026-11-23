package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"text/tabwriter"

	"github.com/mycophonic/primordium/filesystem"

	"github.com/duncanite/go-bosch/homeconnect/account"
	"github.com/duncanite/go-bosch/homeconnect/profile"
	"github.com/duncanite/go-bosch/internal/terminal"
)

// largestProfile bounds the file a profile is read from.
const largestProfile = 16 << 20

// filed is an appliance's id that can be a file's name as it is: the
// account's word is not trusted with a path.
var filed = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// described reads the profile --profile names; nil with none named.
func (root *rootCommand) described() (*profile.Profile, error) {
	if root.Profile == "" {
		return nil, nil //nolint:nilnil // no profile named is no error, and the command goes on without names
	}

	file, err := os.Open(root.Profile)
	if err != nil {
		return nil, fmt.Errorf("the profile: %w", err)
	}

	defer func() { _ = file.Close() }()

	archive, err := io.ReadAll(io.LimitReader(file, largestProfile+1))
	if err != nil {
		return nil, fmt.Errorf("the profile %s: %w", root.Profile, err)
	}

	read, err := profile.FromArchive(archive)
	if err != nil {
		return nil, fmt.Errorf("the profile %s: %w", root.Profile, err)
	}

	return read, nil
}

// named is how a value's id is written: the name the profile gives it,
// or the id itself.
func named(read *profile.Profile, uid string) string {
	if read == nil {
		return uid
	}

	number, err := strconv.Atoi(uid)
	if err != nil {
		return uid
	}

	if feature, known := read.Feature(number); known && feature.Name != "" {
		return feature.Name
	}

	return uid
}

// said is a value as it is written: the name of the member it is, when
// the profile says it is one, or the value on one line as it came.
func said(read *profile.Profile, uid string, value json.RawMessage) string {
	if read != nil {
		if number, err := strconv.Atoi(uid); err == nil {
			if feature, known := read.Feature(number); known {
				if member, is := feature.Says(value); is {
					return member
				}
			}
		}
	}

	return oneLine(value)
}

// Run prints what the profile says the appliance has.
func (*featuresCommand) Run(_ context.Context, root *rootCommand) error {
	read, err := root.described()
	if err != nil {
		return err
	}

	if read == nil {
		return fmt.Errorf("%w: name the appliance's profile with --profile", errUsage)
	}

	table := tabwriter.NewWriter(root.out, 0, 0, 2, ' ', 0)

	terminal.Printf(table, "UID\tKIND\tACCESS\tNAME\n")

	for _, feature := range read.Features() {
		terminal.Printf(
			table,
			"%d\t%s\t%s\t%s\n",
			feature.UID,
			feature.Kind,
			shown(feature.Access),
			shown(feature.Name),
		)
	}

	if err = table.Flush(); err != nil {
		return fmt.Errorf("writing the features: %w", err)
	}

	return nil
}

// save fetches each appliance's profile and writes it into a directory,
// under the appliance's id. One that cannot be had or written is said,
// and the others are written all the same.
func (root *rootCommand) save(
	ctx context.Context, client *account.Client, session account.Session, appliances []account.Appliance, dir string,
) error {
	const private = 0o700

	if err := os.MkdirAll(dir, private); err != nil {
		return fmt.Errorf("the directory for the profiles: %w", err)
	}

	var failed []error

	for _, appliance := range appliances {
		if !filed.MatchString(appliance.ID) {
			failed = append(failed, fmt.Errorf("%w: the id %q is no name for a file", errUsage, appliance.ID))

			continue
		}

		archive, err := client.Profile(ctx, session, appliance.ID)
		if err == nil {
			// Read before it is written: what is no profile is not kept
			// as one.
			_, err = profile.FromArchive(archive)
		}

		if err == nil {
			const owner = 0o600

			err = filesystem.WriteFile(filepath.Join(dir, appliance.ID+".zip"), archive, owner)
		}

		if err != nil {
			failed = append(failed, fmt.Errorf("the profile of %s: %w", appliance.ID, err))

			continue
		}

		terminal.Printf(root.err, "Wrote %s\n", filepath.Join(dir, appliance.ID+".zip"))
	}

	return errors.Join(failed...)
}
