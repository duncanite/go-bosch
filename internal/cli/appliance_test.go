package cli_test

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/forkcloser/go-mdns/mdnstest"

	"github.com/duncanite/go-bosch/homeconnect/homeconnecttest"
	"github.com/duncanite/go-bosch/internal/cli"
)

// wait bounds what a test waits for.
const wait = 20 * time.Second

func started(t *testing.T) *homeconnecttest.Appliance {
	t.Helper()

	fake := homeconnecttest.New()
	t.Cleanup(fake.Close)

	return fake
}

// key is the test appliance's key, as the environment holds it.
func key() map[string]string {
	return map[string]string{cli.EnvKey: homeconnecttest.Key, cli.EnvVector: homeconnecttest.Vector}
}

// speak is the command against the fake, at its address, under env; its
// output and what it wrote of errors, then its status.
func speak(
	t *testing.T,
	fake *homeconnecttest.Appliance,
	env map[string]string,
	args ...string,
) (out, errs string, status int) {
	t.Helper()

	var stdout, stderr bytes.Buffer

	status = cli.Run(t.Context(), cli.Environment{
		Args: append([]string{"--host", fake.Addr()}, args...), Out: &stdout, Err: &stderr,
		Getenv: func(name string) string { return env[name] },
	})

	return stdout.String(), stderr.String(), status
}

// TestInfo: what the appliance says of its make and its versions is
// printed, and nothing that names the one appliance; whole, all it said
// is, as JSON. An appliance that was asked nothing of itself says so.
func TestInfo(t *testing.T) {
	t.Parallel()

	fake := started(t)

	out, errs, status := speak(t, fake, key(), "info")
	if want := strings.Join([]string{
		"brand: THERMADOR", "vib: DWHD000XXX", "eNumber: DWHD000XXX/01", "deviceInfo: DISHWASHER",
		"haVersion: 1.0", "swVersion: 2.17.1", "hwVersion: 0000000001", "",
	}, "\n"); status != cli.ExitOK || errs != "" || out != want {
		t.Errorf("info exits %d, saying %q and\n%s\nwant\n%s", status, errs, out, want)
	}

	out, errs, status = speak(t, fake, key(), "info", "--whole")
	if status != cli.ExitOK || errs != "" || !strings.HasPrefix(out, "{\n  \"") ||
		!strings.Contains(out, `"serialNumber": "000000000000000001"`) || !strings.HasSuffix(out, "\n}\n") {
		t.Errorf("info --whole exits %d, saying %q and\n%s", status, errs, out)
	}

	quiet := started(t)
	quiet.Serve("ci", 3)
	quiet.Serve("iz", 0)

	out, errs, status = speak(t, quiet, key(), "info")
	if status != cli.ExitOK || errs != "" || out != "The appliance said nothing of itself.\n" {
		t.Errorf("info of an appliance that says nothing of itself exits %d, saying %q and %q", status, errs, out)
	}
}

// TestServicesAndValues: the services are printed with their versions,
// in order; the values with their ids, each as the appliance wrote it, on
// one line, with nothing in it a terminal obeys. An answer of another
// shape is printed whole.
func TestServicesAndValues(t *testing.T) {
	t.Parallel()

	fake := started(t)

	out, errs, status := speak(t, fake, key(), "services")
	if want := "SERVICE VERSION\nci 2\nei 2\niz 1\nni 1\nro 1"; status != cli.ExitOK || errs != "" ||
		rows(out) != want {
		t.Errorf("services exits %d, saying %q and\n%s", status, errs, rows(out))
	}

	fake.Set(539, 2)
	fake.Set(552, "run\x1b[2J\nUID 1")
	fake.Set(5, true)
	fake.Set(600, map[string]any{"a": []int{1, 2}})
	fake.Set(601, nil)

	out, errs, status = speak(t, fake, key(), "values")
	if want := strings.Join([]string{
		"UID VALUE",
		"5 true",
		"539 2",
		`552 "run\u001b[2J\nUID 1"`,
		`600 {"a":[1,2]}`,
		"601 null",
	}, "\n"); status != cli.ExitOK || errs != "" || rows(out) != want {
		t.Errorf("values exits %d, saying %q and\n%s\nwant\n%s", status, errs, rows(out), want)
	}

	odd := started(t)
	odd.Refuse("/ro/allMandatoryValues", 500)

	out, errs, status = speak(t, odd, key(), "values")
	if status != cli.ExitError || out != "" || !strings.Contains(errs, "500, InternalServerError") {
		t.Errorf("values of an appliance that refuses them exits %d, saying %q and %q", status, out, errs)
	}
}

// TestCall: any request is sent as it is typed, and its answer printed
// as JSON a person reads; a post sets, a notify prints nothing. Data that
// is no JSON is refused before anything is sent, and an appliance's
// refusal is the error, with its code.
func TestCall(t *testing.T) {
	t.Parallel()

	fake := started(t)
	fake.Set(5, true)

	out, errs, status := speak(t, fake, key(), "call", "get", "/ro/allMandatoryValues")
	if want := "[\n  {\n    \"uid\": 5,\n    \"value\": true\n  }\n]\n"; status != cli.ExitOK || errs != "" ||
		out != want {
		t.Errorf("call get exits %d, saying %q and\n%s", status, errs, out)
	}

	out, errs, status = speak(t, fake, key(), "call", "post", "/ro/values", `{"uid":5,"value":false}`)
	if status != cli.ExitOK || errs != "" || out != "" {
		t.Errorf("call post exits %d, saying %q and %q", status, errs, out)
	}

	out, _, _ = speak(t, fake, key(), "call", "get", "/ro/allMandatoryValues")
	if !strings.Contains(out, `"value": false`) {
		t.Errorf("after the post the appliance holds\n%s", out)
	}

	before := len(fake.Received())

	out, errs, status = speak(t, fake, key(), "call", "notify", "/ei/deviceReady")
	if status != cli.ExitOK || errs != "" || out != "" {
		t.Errorf("call notify exits %d, saying %q and %q", status, errs, out)
	}

	// The appliance reads what was sent in its own time.
	deadline := time.Now().Add(wait)
	for len(fake.Received()) < before+8 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	if sent := fake.Received()[before:]; len(sent) != 8 || sent[7].Action != "NOTIFY" ||
		sent[7].Resource != "/ei/deviceReady" {
		t.Errorf("call notify sent %+v", sent)
	}

	before = len(fake.Received())

	out, errs, status = speak(t, fake, key(), "call", "post", "/ro/values", `{"uid":`)
	if status != cli.ExitError || out != "" || !strings.Contains(errs, "the data is not JSON") ||
		len(fake.Received()) != before {
		t.Errorf("call with data that is no JSON exits %d, saying %q and %q", status, out, errs)
	}

	out, errs, status = speak(t, fake, key(), "call", "get", "/xx/nothing")
	if status != cli.ExitError || out != "" || !strings.Contains(errs, "/xx/nothing: 404, NotFound") {
		t.Errorf("call of what the appliance has not exits %d, saying %q and %q", status, out, errs)
	}
}

// TestTheKey: the key is the environment's. What is not set is named; a
// key that is none, and one that opens nothing, are said, and none of
// them printed. A timeout of nothing is refused.
func TestTheKey(t *testing.T) {
	t.Parallel()

	fake := started(t)

	for name, row := range map[string]struct {
		env  map[string]string
		want string
	}{
		"nothing set":    {nil, "not set in the environment: HOMECONNECT_KEY, HOMECONNECT_IV"},
		"no vector":      {map[string]string{cli.EnvKey: homeconnecttest.Key}, "not set in the environment: HOMECONNECT_IV"},
		"a key not one":  {map[string]string{cli.EnvKey: "!!secret!!", cli.EnvVector: homeconnecttest.Vector}, "the key is not base64"},
		"another's key":  {map[string]string{cli.EnvKey: strings.Repeat("A", 43), cli.EnvVector: homeconnecttest.Vector}, "the key does not open what answers there"},
		"a short vector": {map[string]string{cli.EnvKey: homeconnecttest.Key, cli.EnvVector: "c2hvcnQ"}, "the initial vector is not"},
	} {
		out, errs, status := speak(t, fake, row.env, "info")
		if status != cli.ExitError || out != "" || !strings.Contains(errs, row.want) ||
			strings.Contains(
				errs,
				"!!secret!!",
			) || strings.Contains(errs, homeconnecttest.Key) || strings.Contains(errs, "AAAA") {
			t.Errorf("%s: info exits %d, saying %q and %q; want an error with %q", name, status, out, errs, row.want)
		}
	}

	// What is on the network asks for no key.
	var (
		network        mdnstest.Network
		stdout, stderr bytes.Buffer
	)

	if status := cli.Run(t.Context(), cli.Environment{
		Args: []string{"--wait", "300ms", "discover"}, Out: &stdout, Err: &stderr, Network: here(&network),
	}); status != cli.ExitOK || stderr.String() != "" || stdout.String() != "No appliance answered.\n" {
		t.Errorf("discover with no key exits %d, saying %q and %q", status, stderr.String(), stdout.String())
	}

	out, errs, status := speak(t, fake, key(), "--timeout", "0s", "info")
	if status != cli.ExitError || out != "" || !strings.Contains(errs, "--timeout wants a time above zero") {
		t.Errorf("a timeout of nothing exits %d, saying %q and %q", status, out, errs)
	}
}

// buffer is a buffer the command and the test can both use.
type buffer struct {
	mu   sync.Mutex
	held bytes.Buffer
}

func (b *buffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.held.Write(data)
}

func (b *buffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.held.String()
}

// await waits for the command to have printed a text.
func await(t *testing.T, out *buffer, text string) {
	t.Helper()

	deadline := time.Now().Add(wait)
	for !strings.Contains(out.String(), text) {
		if time.Now().After(deadline) {
			t.Fatalf("waited for %q; the command printed:\n%s", text, out.String())
		}

		time.Sleep(5 * time.Millisecond)
	}
}

// connected waits for the fake to have a client.
func connected(t *testing.T, fake *homeconnecttest.Appliance) {
	t.Helper()

	deadline := time.Now().Add(wait)
	for len(fake.Received()) < 7 {
		if time.Now().After(deadline) {
			t.Fatal("the command did not open a session")
		}

		time.Sleep(5 * time.Millisecond)
	}
}

// TestWatch: each thing the appliance says is printed as it comes, on one
// line: what it is about and what it carries; what cannot be read is
// printed as it came, and the watch goes on. Interrupted, the command
// ends with nothing wrong; an appliance that hangs up is an error.
func TestWatch(t *testing.T) {
	t.Parallel()

	fake := started(t)

	// watching is the command watching the fake: what it prints, what it
	// writes of errors, and its status once it ends.
	watching := func(ctx context.Context, out, errs *buffer) chan int {
		done := make(chan int, 1)

		go func() {
			done <- cli.Run(ctx, cli.Environment{
				Args: []string{"--host", fake.Addr(), "watch"}, Out: out, Err: errs,
				Getenv: func(name string) string { return key()[name] },
			})
		}()

		return done
	}

	ctx, interrupt := context.WithCancel(t.Context())
	defer interrupt()

	out, errs := new(buffer), new(buffer)
	done := watching(ctx, out, errs)

	connected(t, fake)

	fake.Set(552, "run")
	await(t, out, `  NOTIFY /ro/values  [{"uid":552,"value":"run"}]`+"\n")

	fake.SayText("not\x1b a message\n{")
	await(t, out, `  unread  not\x1b a message\n{`+"\n")

	fake.Set(552, "\x1b[2J")
	await(t, out, `[{"uid":552,"value":"\u001b[2J"}]`+"\n")

	interrupt()

	if status := <-done; status != cli.ExitOK || errs.String() != "" || strings.Contains(out.String(), "\x1b") {
		t.Errorf("an interrupted watch exits %d, saying %q and\n%s", status, errs.String(), out.String())
	}

	if lines := strings.Count(out.String(), "\n"); lines != 3 {
		t.Errorf("the watch printed %d lines:\n%s", lines, out.String())
	}

	before := len(fake.Received())
	out, errs = new(buffer), new(buffer)
	done = watching(t.Context(), out, errs)

	deadline := time.Now().Add(wait)
	for len(fake.Received()) < before+7 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	fake.Drop()

	if status := <-done; status != cli.ExitError ||
		!strings.Contains(errs.String(), "the connection to the appliance is lost") {
		t.Errorf("a watch the appliance hangs up on exits %d, saying %q and %q", status, errs.String(), out.String())
	}
}

// TestTheApplianceSearchedFor: with no host given, the one appliance on
// the network is spoken to, at the address and the port it announces.
// Among several none is chosen, and they are named; --id says which. One
// that does not answer the search is said so, with the flag that needs
// none.
func TestTheApplianceSearchedFor(t *testing.T) {
	t.Parallel()

	fake := started(t)

	var network mdnstest.Network

	if out, errs, status := run(t, here(&network), "--wait", "500ms", "info"); status != cli.ExitError || out != "" ||
		!strings.Contains(errs, "no appliance answered the search within 500ms") || !strings.Contains(errs, "--host") {
		t.Errorf("info with no appliance on the network exits %d, saying %q and %q", status, out, errs)
	}

	announcing(
		t,
		&network,
		"THERMADOR-DWHD000XXX-000000000001",
		fake.Addr(),
		entries("id", "THERMADOR-DWHD000XXX-000000000001"),
	)

	out, errs, status := run(t, here(&network), "--wait", "2s", "info")
	if status != cli.ExitOK || errs != "" || !strings.HasPrefix(out, "brand: THERMADOR\n") {
		t.Errorf("info of the one appliance on the network exits %d, saying %q and %q", status, errs, out)
	}

	// Another appliance, where nothing listens.
	announcing(
		t,
		&network,
		"BOSCH-WAX000XXX-000000000002",
		"192.0.2.10:80",
		entries("id", "BOSCH-WAX000XXX-000000000002"),
	)

	out, errs, status = run(t, here(&network), "--wait", "2s", "info")
	if status != cli.ExitError || out != "" || !strings.Contains(
		errs,
		"2 appliances answered the search, BOSCH-WAX000XXX-000000000002, THERMADOR-DWHD000XXX-000000000001; name one with --id",
	) {
		t.Errorf("info among two appliances exits %d, saying %q and %q", status, out, errs)
	}

	// A wait as long as the command has: the search ends once the one
	// named is heard, or the command fails.
	out, errs, status = run(t, here(&network), "--wait", "15s", "--id", "THERMADOR-DWHD000XXX-000000000001", "info")
	if status != cli.ExitOK || errs != "" || !strings.HasPrefix(out, "brand: THERMADOR\n") {
		t.Errorf("info of the appliance named exits %d, saying %q and %q", status, errs, out)
	}

	out, errs, status = run(t, here(&network), "--wait", "500ms", "--id", "SIEMENS-XX-000000000003", "info")
	if status != cli.ExitError || out != "" ||
		!strings.Contains(errs, "the appliance SIEMENS-XX-000000000003 did not answer the search within 500ms") {
		t.Errorf("info of an appliance that is not there exits %d, saying %q and %q", status, out, errs)
	}
}
