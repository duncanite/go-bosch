package cli_test

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/duncanite/go-bosch/homeconnect/account"
	"github.com/duncanite/go-bosch/homeconnect/account/accounttest"
	"github.com/duncanite/go-bosch/internal/cli"
)

// A profile's two documents, cut down to a door, an operation state, a
// remaining time, and a feature whose name is a stranger's text.
const (
	described = `<device>
  <statusList uid="0100">
    <status access="read" enumerationType="0201" uid="020F"/>
    <status access="read" enumerationType="0228" uid="0228"/>
    <status access="read" uid="0220"/>
  </statusList>
  <settingList uid="0102"><setting access="readWrite" uid="1100"/></settingList>
</device>`

	mapped = `<featureMappingFile>
  <featureDescription>
    <feature refUID="020F">BSH.Common.Status.DoorState</feature>
    <feature refUID="0228">BSH.Common.Status.OperationState</feature>
    <feature refUID="0220">BSH.Common.Option.RemainingProgramTime</feature>
    <feature refUID="1100">Forged&#10;12:00:00  BSH.Door = &#8238;Open</feature>
  </featureDescription>
  <enumDescriptionList>
    <enumDescription refENID="0201"><enumMember refValue="0">Open</enumMember><enumMember refValue="1">Closed</enumMember></enumDescription>
    <enumDescription refENID="0228"><enumMember refValue="1">Ready</enumMember><enumMember refValue="3">Run</enumMember></enumDescription>
  </enumDescriptionList>
</featureMappingFile>`
)

// packed is a profile's archive, as the account gives it.
func packed(t *testing.T) []byte {
	t.Helper()

	var archive bytes.Buffer

	writer := zip.NewWriter(&archive)

	for name, content := range map[string]string{"A-0001_DeviceDescription.xml": described, "A-0001_FeatureMapping.xml": mapped} {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}

		if _, err = entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}

	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	return archive.Bytes()
}

// profiled is a file holding a profile's archive.
func profiled(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "A-0001.zip")
	if err := os.WriteFile(path, packed(t), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

// TestFeatures: what the profile says the appliance has is printed, each
// feature with its id, its kind, its access and its name, with no key
// and no appliance; a name that is a stranger's text forges no line. No
// profile named, and a file that is none, are said.
func TestFeatures(t *testing.T) {
	t.Parallel()

	fake := started(t)

	out, errs, status := speak(t, fake, nil, "--profile", profiled(t), "features")
	if want := strings.Join([]string{
		"UID KIND ACCESS NAME",
		"527 status read BSH.Common.Status.DoorState",
		"544 status read BSH.Common.Option.RemainingProgramTime",
		"552 status read BSH.Common.Status.OperationState",
		`4352 setting readwrite Forged\n12:00:00 BSH.Door = \u202eOpen`,
	}, "\n"); status != cli.ExitOK || errs != "" || rows(out) != want {
		t.Errorf("features exits %d, saying %q and\n%s\nwant\n%s", status, errs, rows(out), want)
	}

	if len(fake.Received()) != 0 {
		t.Errorf("features spoke to the appliance: %+v", fake.Received())
	}

	out, errs, status = speak(t, fake, nil, "features")
	if status != cli.ExitError || out != "" || !strings.Contains(errs, "--profile") {
		t.Errorf("features with no profile exits %d, saying %q and %q", status, out, errs)
	}

	none := filepath.Join(t.TempDir(), "none.zip")
	if err := os.WriteFile(none, []byte("not an archive"), 0o600); err != nil {
		t.Fatal(err)
	}

	for name, path := range map[string]string{"what is no archive": none, "no file": filepath.Join(t.TempDir(), "absent.zip")} {
		out, errs, status = speak(t, fake, key(), "--profile", path, "values")
		if status != cli.ExitError || out != "" || !strings.Contains(errs, "the profile") {
			t.Errorf("values under a profile that is %s exits %d, saying %q and %q", name, status, out, errs)
		}
	}
}

// TestNamedValues: under a profile, values are printed with the names it
// gives their ids, a dash for one it does not name, and each as the
// member it is when the profile says so; a number that is no member is
// printed as it came.
func TestNamedValues(t *testing.T) {
	t.Parallel()

	fake := started(t)
	fake.Set(527, 0)
	fake.Set(552, 3)
	fake.Set(544, 7740)
	fake.Set(999, "raw")
	fake.Set(4352, true)

	out, errs, status := speak(t, fake, key(), "--profile", profiled(t), "values")
	if want := strings.Join([]string{
		"UID NAME VALUE",
		"527 BSH.Common.Status.DoorState Open",
		"544 BSH.Common.Option.RemainingProgramTime 7740",
		"552 BSH.Common.Status.OperationState Run",
		`999 - "raw"`,
		`4352 Forged\n12:00:00 BSH.Door = \u202eOpen true`,
	}, "\n"); status != cli.ExitOK || errs != "" || rows(out) != want {
		t.Errorf("values under a profile exits %d, saying %q and\n%s\nwant\n%s", status, errs, rows(out), want)
	}

	fake.Set(527, 7)

	out, _, _ = speak(t, fake, key(), "--profile", profiled(t), "values")
	if !strings.Contains(rows(out), "527 BSH.Common.Status.DoorState 7\n") {
		t.Errorf("a door at a number that is no member is printed as\n%s", rows(out))
	}
}

// TestNamedWatch: under a profile, each value that changes is a line of
// its own, by its name and as the member it is; a change to an id the
// profile does not name is printed by the id; what is no value is
// printed as it came.
func TestNamedWatch(t *testing.T) {
	t.Parallel()

	fake := started(t)
	path := profiled(t)

	ctx, interrupt := context.WithCancel(t.Context())
	defer interrupt()

	out, errs := new(buffer), new(buffer)
	done := make(chan int, 1)

	go func() {
		done <- cli.Run(ctx, cli.Environment{
			Args: []string{"--host", fake.Addr(), "--profile", path, "watch"}, Out: out, Err: errs,
			Getenv: func(name string) string { return key()[name] },
		})
	}()

	connected(t, fake)

	fake.Set(527, 1)
	await(t, out, "  BSH.Common.Status.DoorState = Closed\n")

	fake.Set(999, 5)
	await(t, out, "  999 = 5\n")

	fake.Set(4352, false)
	await(t, out, `  Forged\n12:00:00  BSH.Door = \u202eOpen = false`+"\n")

	fake.SayText(
		`{"sID":1,"msgID":7,"resource":"/ro/descriptionChange","version":1,"action":"NOTIFY","data":[{"uid":527,"access":"READ"}]}`,
	)
	await(t, out, `  NOTIFY /ro/descriptionChange  [{"uid":527,"access":"READ"}]`+"\n")

	interrupt()

	if status := <-done; status != cli.ExitOK || errs.String() != "" || strings.Count(out.String(), "\n") != 4 ||
		strings.Contains(out.String(), "\u202e") {
		t.Errorf("a watch under a profile exits %d, saying %q and\n%s", status, errs.String(), out.String())
	}
}

// TestAccountProfiles: with a directory named, account writes each
// appliance's profile there under its id, readable by its owner alone,
// the very bytes the account gave. One the account has not, one that is
// no profile, and an appliance whose id is no name for a file are each
// said, and the others written all the same.
func TestAccountProfiles(t *testing.T) {
	t.Parallel()

	api := accounttest.New()
	t.Cleanup(api.Close)

	for _, id := range []string{"A-0001", "B-0002", "C-0003", "../D-0004"} {
		api.Add(account.Appliance{ID: id, Mode: account.AES, Key: "a-key", Vector: "a-vector"})
	}

	// One archive, kept: two made alike need not be the same bytes.
	given := packed(t)

	api.Describe("A-0001", given)
	api.Describe("C-0003", []byte("PK no profile"))
	api.Describe("../D-0004", given)

	dir := filepath.Join(t.TempDir(), "profiles")

	var stdout bytes.Buffer

	said, typed := new(buffer), newPipe()

	go func() {
		defer typed.close()

		await(t, said, "press Enter:")
		typed.write(api.Browse(opened.FindString(said.String())) + "\n")
	}()

	status := cli.Run(t.Context(), cli.Environment{
		Args: []string{"account", "--profiles", dir}, In: typed.reader, Out: &stdout, Err: said,
		Account: account.Options{Login: api.URL(), Assets: api.URL()},
	})

	if status != cli.ExitError || !strings.Contains(stdout.String(), "A-0001") {
		t.Fatalf("account --profiles exits %d, saying %q and %q", status, stdout.String(), said.String())
	}

	written, err := os.ReadFile(filepath.Join(dir, "A-0001.zip"))
	if err != nil || !bytes.Equal(written, given) {
		t.Errorf("the profile written is %d bytes, %v", len(written), err)
	}

	if info, statErr := os.Stat(filepath.Join(dir, "A-0001.zip")); statErr != nil ||
		(runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
		t.Errorf("the profile is written as %v, %v", info, statErr)
	}

	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Errorf("the directory holds %v, %v; want the one profile", entries, err)
	}

	if _, err = os.Stat(filepath.Join(filepath.Dir(dir), "D-0004.zip")); err == nil {
		t.Error("a profile was written outside the directory")
	}

	for _, want := range []string{
		"the profile of B-0002", "the profile of C-0003", `the id "../D-0004" is no name for a file`, "Wrote ",
	} {
		if !strings.Contains(said.String(), want) {
			t.Errorf("account --profiles did not say %q:\n%s", want, said.String())
		}
	}
}
