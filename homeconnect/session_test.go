package homeconnect_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/duncanite/go-bosch/homeconnect"
	"github.com/duncanite/go-bosch/homeconnect/homeconnecttest"
)

// wait bounds what a test waits for.
const wait = 20 * time.Second

func started(t *testing.T) *homeconnecttest.Appliance {
	t.Helper()

	fake := homeconnecttest.New()
	t.Cleanup(fake.Close)

	return fake
}

func dialed(t *testing.T, fake *homeconnecttest.Appliance) *homeconnect.Session {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), wait)
	defer cancel()

	session, err := homeconnect.Dial(ctx, fake.Addr(), fake.Key())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	t.Cleanup(func() { _ = session.Close() })

	return session
}

// asked is what a client sent, each as its action and its resource.
func asked(messages []homeconnect.Message) []string {
	all := make([]string, 0, len(messages))
	for _, message := range messages {
		all = append(all, string(message.Action)+" "+message.Resource)
	}

	return all
}

// TestDial: the appliance opens the session, and the client answers by
// naming itself, its kind, its name and its id in that order, under the
// appliance's own id for that message; then asks for the services, with
// version 1, and does what each wants, numbering its messages from where
// the appliance said and under the session's id. The services and what
// the appliance said of itself are kept.
func TestDial(t *testing.T) {
	t.Parallel()

	fake := started(t)
	session := dialed(t, fake)

	received := fake.Received()

	if want := []string{
		"RESPONSE /ei/initialValues", "GET /ci/services", "GET /ci/authentication", "GET /ci/info",
		"GET /iz/info", "NOTIFY /ei/deviceReady", "GET /ni/info",
	}; !reflect.DeepEqual(asked(received), want) {
		t.Fatalf("the handshake sent %v, want %v", asked(received), want)
	}

	if want := `{"sID":1,"msgID":1000,"resource":"/ei/initialValues","version":2,"action":"RESPONSE",` +
		`"data":[{"deviceType":"Application","deviceName":"go-bosch","deviceID":"676f2d626f736368"}]}`; fake.Greeted()[0] != want {
		t.Errorf("the client named itself by\n%s\nwant\n%s", fake.Greeted()[0], want)
	}

	for index, message := range received[1:] {
		if message.Session != 1 || message.ID != int64(homeconnecttest.FirstID+index) {
			t.Errorf("%s %s went under session %d as message %d, want session 1 and message %d",
				message.Action, message.Resource, message.Session, message.ID, homeconnecttest.FirstID+index)
		}
	}

	if services := received[1]; services.Version != 1 || services.Data != nil {
		t.Errorf("the services were asked for as %+v, want version 1 and no data", services)
	}

	var nonce []struct {
		Nonce string `json:"nonce"`
	}

	if err := json.Unmarshal(received[2].Data, &nonce); err != nil || len(nonce) != 1 || len(nonce[0].Nonce) != 43 ||
		strings.ContainsAny(nonce[0].Nonce, "=+/") {
		t.Errorf("the nonce was sent as %s", received[2].Data)
	}

	// The command service is at version 2: what follows is asked at it.
	if info := received[3]; info.Version != 2 {
		t.Errorf("/ci/info was asked at version %d, want the service's 2", info.Version)
	}

	if want := map[string]int{
		"ci": 2,
		"ei": 2,
		"iz": 1,
		"ni": 1,
		"ro": 1,
	}; !reflect.DeepEqual(
		session.Services(),
		want,
	) {
		t.Errorf("Services = %v, want %v", session.Services(), want)
	}

	var itself map[string]any
	if err := json.Unmarshal(session.Identity(), &itself); err != nil || itself["brand"] != "THERMADOR" {
		t.Errorf("Identity = %s, %v", session.Identity(), err)
	}

	if session.Err() != nil {
		t.Errorf("a session that stands has Err %v", session.Err())
	}
}

// TestDialAsTheServicesWant: an appliance whose first service is at
// version 1 is told the client's kind by a number, and not that the
// client is ready; one whose command service is at version 3 is not
// authenticated to; one with no identification or network service is not
// asked about them. A refusal to say what it is, or of the network's
// question, leaves the session standing; a refusal of the authentication
// or of the identification does not.
func TestDialAsTheServicesWant(t *testing.T) {
	t.Parallel()

	fake := started(t)
	fake.Serve("ei", 1)
	fake.Serve("ci", 3)
	fake.Serve("iz", 0)
	fake.Serve("ni", 0)

	session := dialed(t, fake)

	// A request and its answer, so that all the handshake sent has been
	// read by the appliance, a notification with no answer among it.
	if _, err := session.Get(t.Context(), "/ro/allDescriptionChanges", nil); err != nil {
		t.Fatal(err)
	}

	want := []string{"RESPONSE /ei/initialValues", "GET /ci/services", "GET /ro/allDescriptionChanges"}
	if sent := asked(fake.Received()); !reflect.DeepEqual(sent, want) {
		t.Errorf("the handshake sent %v, want %v", sent, want)
	}

	if greeted := fake.Greeted()[0]; !strings.Contains(greeted, `"version":1,`) ||
		!strings.Contains(greeted, `"data":[{"deviceType":2,"deviceName":`) {
		t.Errorf("under version 1 the client named itself by %s", greeted)
	}

	if session.Identity() != nil {
		t.Errorf("Identity of an appliance that was not asked = %s", session.Identity())
	}

	tolerant := started(t)
	tolerant.Refuse("/ci/info", 404)
	tolerant.Refuse("/ni/info", 500)

	if stood := dialed(t, tolerant); stood.Err() != nil {
		t.Errorf("a session whose appliance refuses /ci/info and /ni/info: %v", stood.Err())
	}

	for _, resource := range []string{"/ci/authentication", "/iz/info", "/ci/services"} {
		strict := started(t)
		strict.Refuse(resource, 403)

		ctx, cancel := context.WithTimeout(t.Context(), wait)
		refusedSession, err := homeconnect.Dial(ctx, strict.Addr(), strict.Key())

		cancel()

		var refused *homeconnect.RefusedError
		if !errors.As(err, &refused) || refused.Code != 403 || refused.Resource != resource ||
			!errors.Is(err, homeconnect.ErrRefused) || refusedSession != nil {
			t.Errorf("Dial of an appliance that refuses %s = %v", resource, err)
		}

		deadline := time.Now().Add(wait)
		for strict.Connected() != 0 && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}

		if strict.Connected() != 0 {
			t.Errorf("a Dial that failed at %s left a connection at the appliance", resource)
		}
	}
}

// TestKeys: a key that is another appliance's opens nothing, and says so
// as ErrKey; one with no vector is an appliance's of the mode this module
// does not speak; no key is no argument. A key parses with or without
// padding, and shows nothing of itself however it is printed.
func TestKeys(t *testing.T) {
	t.Parallel()

	fake := started(t)

	other, err := homeconnect.ParseKey("YW5vdGhlciBhcHBsaWFuY2UncyBrZXksIDMyIGJ5dGU", homeconnecttest.Vector)
	if err != nil {
		t.Fatal(err)
	}

	elsewhere, err := homeconnect.ParseKey(homeconnecttest.Key, "YW5vdGhlciB2ZWN0b3IhIQ")
	if err != nil {
		t.Fatal(err)
	}

	tls, err := homeconnect.ParseKey(homeconnecttest.Key, "")
	if err != nil {
		t.Fatal(err)
	}

	for name, row := range map[string]struct {
		key  homeconnect.Key
		want error
	}{
		"another key":    {other, homeconnect.ErrKey},
		"another vector": {elsewhere, homeconnect.ErrKey},
		"no vector":      {tls, homeconnect.ErrUnsupported},
		"no key":         {homeconnect.Key{}, homeconnect.ErrArgument},
	} {
		ctx, cancel := context.WithTimeout(t.Context(), wait)
		session, dialErr := homeconnect.Dial(ctx, fake.Addr(), row.key)

		cancel()

		if !errors.Is(dialErr, row.want) || session != nil {
			t.Errorf("Dial under %s = %v, want %v", name, dialErr, row.want)
		}
	}

	padded, err := homeconnect.ParseKey(homeconnecttest.Key+"=", homeconnecttest.Vector+"==")
	if err != nil {
		t.Fatalf("a padded key: %v", err)
	}

	if session, dialErr := homeconnect.Dial(t.Context(), fake.Addr(), padded); dialErr != nil {
		t.Errorf("Dial under the key with its padding: %v", dialErr)
	} else {
		_ = session.Close()
	}

	for name, pair := range map[string][2]string{
		"no key":             {"", homeconnecttest.Vector},
		"a key not base64":   {"not base64!", homeconnecttest.Vector},
		"a vector too short": {homeconnecttest.Key, "c2hvcnQ"},
		"a vector not that":  {homeconnecttest.Key, "****"},
	} {
		if _, err = homeconnect.ParseKey(pair[0], pair[1]); !errors.Is(err, homeconnect.ErrArgument) {
			t.Errorf("ParseKey of %s = %v, want ErrArgument", name, err)
		}
	}

	var logged bytes.Buffer

	slog.New(slog.NewTextHandler(&logged, nil)).Info("opening", "key", fake.Key())

	held := fake.Key()

	// Every verb, the ones fmt would print a key's bytes under among them.
	shown := fmt.Sprintf("%v %+v %#v %s %q %d %x %X", held, held, held, held, held, held, held, held) + logged.String()
	if strings.Contains(shown, "test appliance") || strings.Contains(shown, "116 104 101") ||
		strings.Contains(strings.ToLower(shown), "746865") || strings.Count(shown, "redacted") != 9 {
		t.Errorf("a key prints as %s", shown)
	}

	if raw, marshalErr := json.Marshal(map[string]any{"key": fake.Key()}); marshalErr == nil {
		t.Errorf("a key was written out as %s", raw)
	}
}

// TestRequests: a Get gives the appliance's answer; a Post sets, and the
// appliance tells of what changed, which Next gives; data that is one
// value goes as a list of it. Each goes under the version of its service.
// A refusal is a RefusedError with the appliance's code, named, and comes
// with the answer; what the appliance does not have is its 404.
func TestRequests(t *testing.T) {
	t.Parallel()

	fake := started(t)
	fake.Serve("ro", 3)
	fake.Set(5, true)
	fake.Set(3, "idle")

	session := dialed(t, fake)

	answer, err := session.Get(t.Context(), "/ro/allMandatoryValues", nil)
	if err != nil || string(answer.Data) != `[{"uid":3,"value":"idle"},{"uid":5,"value":true}]` ||
		answer.Action != homeconnect.Response || answer.Version != 3 {
		t.Fatalf("Get of the values = %+v, %v", answer, err)
	}

	before := len(fake.Received())

	if _, err = session.Post(t.Context(), "/ro/values", map[string]any{"uid": 5, "value": false}); err != nil {
		t.Fatalf("Post: %v", err)
	}

	sent := fake.Received()[before]
	if sent.Action != homeconnect.Post || string(sent.Data) != `[{"uid":5,"value":false}]` || sent.Version != 3 {
		t.Errorf("the Post went as %+v", sent)
	}

	told, err := session.Next(t.Context())
	if err != nil || told.Action != homeconnect.Notify || told.Resource != "/ro/values" ||
		string(told.Data) != `[{"uid":5,"value":false}]` {
		t.Errorf("Next after the Post = %+v, %v", told, err)
	}

	if err = session.Notify(t.Context(), "/ei/deviceReady", []int{1}); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	fake.Refuse("/ro/activeProgram", 538)

	answer, err = session.Post(t.Context(), "/ro/activeProgram", map[string]any{"program": 1})

	var refused *homeconnect.RefusedError
	if !errors.As(err, &refused) || refused.Code != 538 || refused.Resource != "/ro/activeProgram" ||
		!strings.Contains(err.Error(), "RemoteStartNotActive") || answer.Code != 538 {
		t.Errorf("a refused Post = %+v, %v", answer, err)
	}

	if _, err = session.Get(t.Context(), "/xx/nothing", nil); !errors.As(err, &refused) || refused.Code != 404 {
		t.Errorf("Get of what the appliance has not = %v", err)
	}

	// A code that says all went well is no refusal.
	fake.Refuse("/ro/selectedProgram", 200)

	if answer, err = session.Post(t.Context(), "/ro/selectedProgram", nil); err != nil || answer.Code != 200 {
		t.Errorf("a Post answered with 200 = %+v, %v", answer, err)
	}

	fake.Refuse("/ro/selectedProgram", 0)

	if last := fake.Received(); last[len(last)-2].Version != 1 || last[len(last)-2].Resource != "/xx/nothing" {
		t.Errorf("what belongs to no service the appliance has went as %+v", last[len(last)-2])
	}

	for name, call := range map[string]func() error{
		"a Get of nothing":      func() error { _, e := session.Get(t.Context(), "", nil); return e },
		"a Notify of nothing":   func() error { return session.Notify(t.Context(), "", nil) },
		"data that is not JSON": func() error { _, e := session.Post(t.Context(), "/ro/values", func() {}); return e },
	} {
		if err = call(); !errors.Is(err, homeconnect.ErrArgument) {
			t.Errorf("%s = %v, want ErrArgument", name, err)
		}
	}
}

// TestWhatAnApplianceSays: Next gives each thing the appliance says, in
// order. What cannot be read comes whole, as an UnreadError, and the
// session goes on. A wait that ends leaves the session standing, and so
// does a request whose answer never comes.
func TestWhatAnApplianceSays(t *testing.T) {
	t.Parallel()

	fake := started(t)
	session := dialed(t, fake)

	fake.Set(7, 1)
	fake.SayText(
		`{"sID":"1","msgID":"9","resource":"/ro/values","version":"1","action":"NOTIFY","data":[{"uid":7,"value":2}]}`,
	)
	fake.SayText("not a message {")
	fake.Set(7, 3)

	for _, want := range []string{`[{"uid":7,"value":1}]`, `[{"uid":7,"value":2}]`} {
		told, err := session.Next(t.Context())
		if err != nil || string(told.Data) != want || told.Session != 1 {
			t.Fatalf("Next = %+v, %v; want %s", told, err, want)
		}
	}

	_, err := session.Next(t.Context())

	var unread *homeconnect.UnreadError
	if !errors.As(err, &unread) || string(unread.Message) != "not a message {" ||
		!errors.Is(err, homeconnect.ErrAnswer) {
		t.Fatalf("Next of what is no message = %v", err)
	}

	if told, nextErr := session.Next(t.Context()); nextErr != nil || string(told.Data) != `[{"uid":7,"value":3}]` {
		t.Fatalf("Next after what is no message = %+v, %v", told, nextErr)
	}

	brief, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	if _, err = session.Next(brief); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Next with nothing said = %v, want the context's end", err)
	}

	fake.Stall("/ro/allDescriptionChanges")

	stalled, stop := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer stop()

	if _, err = session.Get(stalled, "/ro/allDescriptionChanges", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Get of what is never answered = %v, want the context's end", err)
	}

	if _, err = session.Get(t.Context(), "/ro/allMandatoryValues", nil); err != nil || session.Err() != nil {
		t.Errorf("a Get after a wait that ended: %v; the session: %v", err, session.Err())
	}

	// A request under a context already ended is not sent, and takes no
	// id: the next one follows the last that went.
	sent := fake.Received()

	gone, cancelled := context.WithCancel(t.Context())
	cancelled()

	for range 3 {
		if _, err = session.Get(gone, "/ro/allMandatoryValues", nil); !errors.Is(err, context.Canceled) {
			t.Errorf("Get under a context that ended = %v, want the context's end", err)
		}
	}

	if _, err = session.Get(t.Context(), "/ro/allMandatoryValues", nil); err != nil {
		t.Fatal(err)
	}

	after := fake.Received()
	if len(after) != len(sent)+1 || after[len(after)-1].ID != sent[len(sent)-1].ID+1 {
		t.Errorf("after three requests that were not sent, the next went as message %d after %d, of %d and %d sent",
			after[len(after)-1].ID, sent[len(sent)-1].ID, len(after), len(sent))
	}
}

// TestARequestGivenUpOn: a request whose caller waits no more is
// forgotten. Its answer, when it comes after all, is not held for a
// caller that left: it is Next's to give, as a Response.
func TestARequestGivenUpOn(t *testing.T) {
	t.Parallel()

	late := started(t)
	session := dialed(t, late)

	brief, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	late.Stall("/ro/allDescriptionChanges")

	if _, err := session.Get(brief, "/ro/allDescriptionChanges", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Get of what is never answered = %v", err)
	}

	asked := late.Received()
	late.Say(homeconnect.Message{
		ID: asked[len(asked)-1].ID, Resource: "/ro/allDescriptionChanges", Version: 1, Action: homeconnect.Response,
	})

	within, stop := context.WithTimeout(t.Context(), wait)
	defer stop()

	if told, err := session.Next(within); err != nil || told.Action != homeconnect.Response ||
		told.ID != asked[len(asked)-1].ID {
		t.Errorf("the answer to a request given up on = %+v, %v; want it from Next", told, err)
	}
}

// ended waits for a session to end and gives why.
func ended(t *testing.T, session *homeconnect.Session) error {
	t.Helper()

	select {
	case <-session.Done():
	case <-time.After(wait):
		t.Fatal("the session did not end")
	}

	return session.Err()
}

// TestEnds: an appliance that hangs up, one that sends a frame out of the
// chain, and one that answers nothing, a ping included, are each a
// session lost; more said than is read ends the session and says so;
// Close ends it as closed, with a request in flight, and every call
// after says the same.
func TestEnds(t *testing.T) {
	t.Parallel()

	dropped := started(t)
	session := dialed(t, dropped)

	dropped.Drop()

	if err := ended(t, session); !errors.Is(err, homeconnect.ErrLost) {
		t.Errorf("an appliance that hangs up: %v, want ErrLost", err)
	}

	if _, err := session.Next(t.Context()); !errors.Is(err, homeconnect.ErrLost) {
		t.Errorf("Next of a session lost = %v", err)
	}

	if _, err := session.Get(t.Context(), "/ci/info", nil); !errors.Is(err, homeconnect.ErrLost) {
		t.Errorf("Get on a session lost = %v", err)
	}

	garbled := started(t)
	session = dialed(t, garbled)

	garbled.Set(1, 1)
	garbled.Garble()

	if err := ended(t, session); !errors.Is(err, homeconnect.ErrLost) || errors.Is(err, homeconnect.ErrKey) {
		t.Errorf("a frame out of the chain: %v, want ErrLost and not the key", err)
	}

	// What was said before it is still read.
	if told, err := session.Next(t.Context()); err != nil || string(told.Data) != `[{"uid":1,"value":1}]` {
		t.Errorf("Next of what came before the session was lost = %+v, %v", told, err)
	}

	deaf := started(t)

	quick, err := homeconnect.Dialer{Heartbeat: 100 * time.Millisecond}.Dial(t.Context(), deaf.Addr(), deaf.Key())
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = quick.Close() })

	// Three heartbeats with an appliance that answers its pings.
	select {
	case <-quick.Done():
		t.Fatalf("a session with an appliance that answers its pings ended: %v", quick.Err())
	case <-time.After(350 * time.Millisecond):
	}

	deaf.Deaf()

	if err = ended(t, quick); !errors.Is(err, homeconnect.ErrLost) {
		t.Errorf("an appliance that answers nothing: %v, want ErrLost", err)
	}

	flooding := started(t)
	session = dialed(t, flooding)

	for value := range 1100 {
		flooding.Set(2, value)
	}

	if err = ended(t, session); !errors.Is(err, homeconnect.ErrOverrun) {
		t.Errorf("more said than was read: %v, want ErrOverrun", err)
	}

	closing := started(t)
	closing.Stall("/ro/allMandatoryValues")
	session = dialed(t, closing)

	flight := make(chan error, 1)

	go func() {
		_, getErr := session.Get(t.Context(), "/ro/allMandatoryValues", nil)
		flight <- getErr
	}()

	deadline := time.Now().Add(wait)
	for len(closing.Received()) < 8 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	if err = session.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}

	if err = <-flight; !errors.Is(err, homeconnect.ErrClosed) {
		t.Errorf("a request in flight at Close = %v, want ErrClosed", err)
	}

	if err = ended(t, session); !errors.Is(err, homeconnect.ErrClosed) {
		t.Errorf("a closed session's Err = %v", err)
	}

	if err = session.Notify(t.Context(), "/ei/deviceReady", nil); !errors.Is(err, homeconnect.ErrClosed) {
		t.Errorf("Notify after Close = %v", err)
	}

	if err = session.Close(); err != nil {
		t.Errorf("Close again: %v", err)
	}
}

// TestMessages: a message is written with the protocol's keys, its data
// and its code only when it has them; it is read with its numbers as
// numbers or as strings, a null for data or for a code as none; what has
// no resource or no action, or a number that is none, is not a message.
func TestMessages(t *testing.T) {
	t.Parallel()

	written, err := json.Marshal(homeconnect.Message{
		Session:  3,
		ID:       44,
		Resource: "/ro/values",
		Version:  1,
		Action:   homeconnect.Post,
		Data:     json.RawMessage(`[{"uid":1}]`),
	})
	if want := `{"sID":3,"msgID":44,"resource":"/ro/values","version":1,"action":"POST","data":[{"uid":1}]}`; err != nil ||
		string(written) != want {
		t.Errorf("a message is written as %s, %v", written, err)
	}

	written, err = json.Marshal(
		homeconnect.Message{Session: 3, ID: 44, Resource: "/x", Version: 1, Action: homeconnect.Response, Code: 404},
	)
	if want := `{"sID":3,"msgID":44,"resource":"/x","version":1,"action":"RESPONSE","code":404}`; err != nil ||
		string(written) != want {
		t.Errorf("a refusal is written as %s, %v", written, err)
	}

	want := homeconnect.Message{Session: 3, ID: 44, Resource: "/x", Version: 2, Action: homeconnect.Response, Code: 404}

	for _, text := range []string{
		`{"sID":3,"msgID":44,"resource":"/x","version":2,"action":"RESPONSE","code":404}`,
		`{"sID":"3","msgID":"44","resource":"/x","version":"2","action":"RESPONSE","code":"404","data":null}`,
	} {
		var read homeconnect.Message
		if err = json.Unmarshal([]byte(text), &read); err != nil || !reflect.DeepEqual(read, want) {
			t.Errorf("%s reads as %+v, %v", text, read, err)
		}
	}

	var plain homeconnect.Message
	if err = json.Unmarshal(
		[]byte(`{"sID":1,"msgID":2,"resource":"/x","version":1,"action":"NOTIFY","code":null}`),
		&plain,
	); err != nil ||
		plain.Code != 0 ||
		plain.Data != nil {
		t.Errorf("a message with a null code reads as %+v, %v", plain, err)
	}

	for name, text := range map[string]string{
		"no resource":      `{"sID":1,"msgID":2,"version":1,"action":"GET"}`,
		"no action":        `{"sID":1,"msgID":2,"resource":"/x","version":1}`,
		"an id of letters": `{"sID":1,"msgID":"two","resource":"/x","version":1,"action":"GET"}`,
		"no object":        `[1,2]`,
	} {
		var read homeconnect.Message
		if err = json.Unmarshal([]byte(text), &read); !errors.Is(err, homeconnect.ErrAnswer) {
			t.Errorf("%s reads as %+v, %v; want ErrAnswer", name, read, err)
		}
	}
}
