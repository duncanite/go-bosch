package cli_test

import (
	"bytes"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/duncanite/go-bosch/homeconnect/account"
	"github.com/duncanite/go-bosch/homeconnect/account/accounttest"
	"github.com/duncanite/go-bosch/internal/cli"
)

// opened finds the address a login is opened at in what the command said.
var opened = regexp.MustCompile(`https?://\S+/security/oauth/authorize\?\S+`)

// signing is the account command against the fake API, with a person's
// part played by answer: it is given the address the command printed,
// and what it returns is pasted. The command's output, what it wrote
// beside it, and its status.
func signing(t *testing.T, api *accounttest.API, answer func(login string) string) (out, errs string, status int) {
	t.Helper()

	var stdout bytes.Buffer

	said, typed := new(buffer), newPipe()

	go func() {
		defer typed.close()

		await(t, said, "press Enter:")

		typed.write(answer(opened.FindString(said.String())) + "\n")
	}()

	status = cli.Run(t.Context(), cli.Environment{
		Args: []string{"account"}, In: typed.reader, Out: &stdout, Err: said,
		Account: account.Options{Login: api.URL(), Assets: api.URL()},
	})

	return stdout.String(), said.String(), status
}

// pipe is what a person types, as the command reads it.
type pipe struct {
	reader *io.PipeReader
	writer *io.PipeWriter
}

func newPipe() pipe {
	reader, writer := io.Pipe()

	return pipe{reader, writer}
}

func (p pipe) write(text string) { _, _ = p.writer.Write([]byte(text)) }
func (p pipe) close()            { _ = p.writer.Close() }

// TestAccount: the command says where to sign in, takes the address the
// browser ends on, and prints each appliance of the account with what
// opens it, a dash for the vector an appliance of the TLS mode has not;
// its instructions go beside its output. An account with no appliance
// says so.
func TestAccount(t *testing.T) {
	t.Parallel()

	api := accounttest.New()
	t.Cleanup(api.Close)

	out, errs, status := signing(t, api, api.Browse)
	if status != cli.ExitOK || out != "The account has no appliance.\n" || !strings.Contains(errs, "hcauth://") {
		t.Errorf("account with no appliance exits %d, saying %q and %q", status, out, errs)
	}

	api.Add(account.Appliance{
		ID: "THERMADOR-DWHD000XXX-000000000001", Brand: "THERMADOR", Type: "Dishwasher", Model: "DWHD000XXX",
		Mode: account.AES, Key: "a-key-of-the-AES-mode", Vector: "its-vector",
	})
	api.Add(account.Appliance{ID: "BOSCH-HBX000XXX-000000000002", Mode: account.TLS, Key: "a-key-of-the-TLS-mode"})

	out, errs, status = signing(t, api, api.Browse)
	if want := strings.Join([]string{
		"ID BRAND TYPE MODEL MODE KEY IV",
		"THERMADOR-DWHD000XXX-000000000001 THERMADOR Dishwasher DWHD000XXX AES a-key-of-the-AES-mode its-vector",
		"BOSCH-HBX000XXX-000000000002 - - - TLS a-key-of-the-TLS-mode -",
	}, "\n"); status != cli.ExitOK || rows(out) != want || strings.Contains(errs, "a-key-of") {
		t.Errorf("account exits %d, saying %q and\n%s\nwant\n%s", status, errs, rows(out), want)
	}
}

// TestAccountRefusals: an appliance whose key is not given is said after
// the others are printed, and the command fails; a login the account
// does not take, nothing pasted, more pasted than an address is, and no
// region are each said, and no key printed.
func TestAccountRefusals(t *testing.T) {
	t.Parallel()

	api := accounttest.New()
	t.Cleanup(api.Close)

	api.Add(account.Appliance{ID: "A-0001", Mode: account.AES, Key: "a-key-of-the-AES-mode", Vector: "its-vector"})
	api.Add(account.Appliance{ID: "B-0002", Mode: account.TLS, Key: "a-key-of-the-TLS-mode"})
	api.Status("/api/appliance/v2/appliances/B-0002/encryption-information", 500)

	out, errs, status := signing(t, api, api.Browse)
	if status != cli.ExitError || !strings.Contains(out, "A-0001") || strings.Contains(out, "B-0002") ||
		!strings.Contains(errs, "B-0002: the account answered with status 500") {
		t.Errorf("account with one key refused exits %d, saying %q and %q", status, out, errs)
	}

	for name, row := range map[string]struct {
		answer func(string) string
		want   string
	}{
		"a code that is none": {func(login string) string { api.Browse(login); return "not the code" }, "the login was not taken"},
		"nothing pasted":      {func(string) string { return "" }, "no code was given"},
		"too much pasted":     {func(string) string { return strings.Repeat("x", 9000) }, "longer than 8192 bytes"},
	} {
		out, errs, status = signing(t, api, row.answer)
		if status != cli.ExitError || out != "" || !strings.Contains(errs, row.want) {
			t.Errorf("%s: account exits %d, saying %q and %q; want an error with %q", name, status, out, errs, row.want)
		}
	}

	var stdout, stderr bytes.Buffer

	status = cli.Run(
		t.Context(),
		cli.Environment{Args: []string{"account"}, In: strings.NewReader(""), Out: &stdout, Err: &stderr},
	)
	if status != cli.ExitError || stdout.String() != "" || !strings.Contains(stderr.String(), "--region") {
		t.Errorf("account with no region exits %d, saying %q and %q", status, stdout.String(), stderr.String())
	}

	stderr.Reset()

	status = cli.Run(t.Context(), cli.Environment{
		Args: []string{"account", "--region", "mars"}, In: strings.NewReader(""), Out: &stdout, Err: &stderr,
	})
	if status != cli.ExitError || !strings.Contains(stderr.String(), "none of eu, na and cn") {
		t.Errorf("account of a region that is none exits %d, saying %q", status, stderr.String())
	}

	stderr.Reset()

	// A region names BSH's own addresses: the login is opened there.
	status = cli.Run(t.Context(), cli.Environment{
		Args: []string{"account", "--region", "na"}, In: strings.NewReader(""), Out: &stdout, Err: &stderr,
	})
	if status != cli.ExitError ||
		!strings.Contains(stderr.String(), "https://api-rna.home-connect.com/security/oauth/authorize?") ||
		!strings.Contains(stderr.String(), "no address was pasted") {
		t.Errorf("account of North America with nothing pasted exits %d, saying %q", status, stderr.String())
	}
}
