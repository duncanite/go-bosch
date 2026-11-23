package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/duncanite/go-bosch/homeconnect/homeconnecttest"
	"github.com/duncanite/go-bosch/internal/cli"
)

// dishwasher is a file holding the test appliance's profile.
func dishwasher(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dishwasher.zip")
	if err := os.WriteFile(path, homeconnecttest.Profile(), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

// TestStatus: what the appliance is doing is printed in words: its
// state, its door, its power, its programs by name, the time left; what
// it has not said is said to be so. With no profile the command says
// what it needs, and speaks to no appliance.
func TestStatus(t *testing.T) {
	t.Parallel()

	fake := started(t)
	fake.Set(homeconnecttest.Operation, 3)
	fake.Set(homeconnecttest.Door, 1)
	fake.Set(homeconnecttest.Power, 2)
	fake.Set(homeconnecttest.SelectedProgram, homeconnecttest.Eco)
	fake.Set(homeconnecttest.ActiveProgram, 7777)
	fake.Set(homeconnecttest.Remaining, 7740)
	fake.Set(homeconnecttest.Progress, 12)

	out, errs, status := speak(t, fake, key(), "--profile", dishwasher(t), "status")
	if want := strings.Join([]string{
		"state: running",
		"door: closed",
		"power: on",
		"selected program: Dishcare.Dishwasher.Program.Eco50",
		"running program: 7777",
		"time left: 2h9m0s",
		"progress: 12",
		"remote start allowed: not said",
		"",
	}, "\n"); status != cli.ExitOK || errs != "" || out != want {
		t.Errorf("status exits %d, saying %q and\n%s\nwant\n%s", status, errs, out, want)
	}

	quiet := started(t)
	quiet.Set(homeconnecttest.ActiveProgram, 0)

	out, _, status = speak(t, quiet, key(), "--profile", dishwasher(t), "status")
	if status != cli.ExitOK || !strings.Contains(out, "state: not said\n") ||
		!strings.Contains(out, "running program: none\n") {
		t.Errorf("status of an appliance that said little exits %d, saying\n%s", status, out)
	}

	before := len(fake.Received())

	out, errs, status = speak(t, fake, key(), "status")
	if status != cli.ExitError || out != "" || !strings.Contains(errs, "--profile") || len(fake.Received()) != before {
		t.Errorf("status with no profile exits %d, saying %q and %q", status, out, errs)
	}
}

// TestDriving: each command that drives the appliance sends its one
// request, prints nothing, and leaves no session behind; a refusal is
// said with the appliance's code.
func TestDriving(t *testing.T) {
	t.Parallel()

	fake := started(t)
	fake.Set(homeconnecttest.SelectedProgram, homeconnecttest.Eco)

	path := dishwasher(t)

	for _, step := range []struct {
		args []string
		want string
	}{
		{[]string{"pause"}, `/ro/values [{"uid":560,"value":true}]`},
		{[]string{"resume"}, `/ro/values [{"uid":561,"value":true}]`},
		{[]string{"stop"}, `/ro/values [{"uid":512,"value":true}]`},
		{[]string{"power", "off"}, `/ro/values [{"uid":539,"value":1}]`},
		{[]string{"power", "on"}, `/ro/values [{"uid":539,"value":2}]`},
		{[]string{"start"}, `/ro/activeProgram [{"options":[],"program":8195}]`},
	} {
		out, errs, status := speak(t, fake, key(), append([]string{"--profile", path}, step.args...)...)
		if status != cli.ExitOK || out != "" || errs != "" {
			t.Fatalf("%v exits %d, saying %q and %q", step.args, status, out, errs)
		}

		var posted []string

		for _, message := range fake.Received() {
			if message.Action == "POST" {
				posted = append(posted, message.Resource+" "+string(message.Data))
			}
		}

		if len(posted) == 0 || posted[len(posted)-1] != step.want {
			t.Errorf("%v sent %v, want last %s", step.args, posted, step.want)
		}
	}

	fake.Refuse("/ro/activeProgram", 538)

	out, errs, status := speak(t, fake, key(), "--profile", path, "start")
	if status != cli.ExitError || out != "" || !strings.Contains(errs, "538, RemoteStartNotActive") {
		t.Errorf("a start the appliance refuses exits %d, saying %q and %q", status, out, errs)
	}

	none := started(t)

	out, errs, status = speak(t, none, key(), "--profile", path, "start")
	if status != cli.ExitError || out != "" || !strings.Contains(errs, "no program is selected") {
		t.Errorf("a start with no program selected exits %d, saying %q and %q", status, out, errs)
	}
}
