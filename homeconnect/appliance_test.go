package homeconnect_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/duncanite/go-bosch/homeconnect"
	"github.com/duncanite/go-bosch/homeconnect/homeconnecttest"
	"github.com/duncanite/go-bosch/homeconnect/profile"
)

// described is the test appliance's profile.
func described(t *testing.T) *profile.Profile {
	t.Helper()

	read, err := profile.FromArchive(homeconnecttest.Profile())
	if err != nil {
		t.Fatalf("the test appliance's profile: %v", err)
	}

	return read
}

// opened is an Appliance over the fake, as it stands when it is opened.
func opened(t *testing.T, fake *homeconnecttest.Appliance) *homeconnect.Appliance {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), wait)
	defer cancel()

	appliance, err := homeconnect.Open(ctx, fake.Addr(), fake.Key(), described(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	t.Cleanup(func() { _ = appliance.Close() })

	return appliance
}

// soon bounds what a test waits for.
func soon(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), wait)
	t.Cleanup(cancel)

	return ctx
}

// TestOpen: an appliance opened is known as far as it has said: what it
// is doing, its door, its power, the time left as a duration, the
// program selected by its id and its name, none active. What it has not
// said is unknown, not zero; what it said in a word this package has not
// is unknown too; and what its profile does not name is passed over.
func TestOpen(t *testing.T) {
	t.Parallel()

	fake := started(t)
	fake.Set(homeconnecttest.Operation, 1)
	fake.Set(homeconnecttest.Door, 1)
	fake.Set(homeconnecttest.Power, 2)
	fake.Set(homeconnecttest.Remaining, 7740)
	fake.Set(homeconnecttest.SelectedProgram, homeconnecttest.Eco)
	fake.Set(homeconnecttest.ActiveProgram, 0)
	fake.Set(9999, "what no profile names")

	state := opened(t, fake).State()

	want := homeconnect.State{
		Operation: new(homeconnect.Ready), Door: new(homeconnect.DoorClosed), Power: new(homeconnect.PowerOn),
		Remaining: new(129 * time.Minute),
		Selected:  &homeconnect.Program{UID: homeconnecttest.Eco, Name: "Dishcare.Dishwasher.Program.Eco50"},
		Active:    &homeconnect.Program{},
	}

	if !reflect.DeepEqual(state, want) {
		t.Errorf("State =\n%s\nwant\n%s", shown(state), shown(want))
	}

	if state.Progress != nil || state.RemoteStart != nil {
		t.Errorf("what the appliance has not said is known as %v and %v", state.Progress, state.RemoteStart)
	}

	odd := started(t)
	odd.Set(homeconnecttest.Operation, 99)
	odd.Set(homeconnecttest.Door, "ajar")
	odd.Set(homeconnecttest.Remaining, -5)
	odd.Set(homeconnecttest.SelectedProgram, 7777)

	state = opened(t, odd).State()
	if state.Operation != nil || state.Door != nil || state.Remaining != nil ||
		!reflect.DeepEqual(state.Selected, &homeconnect.Program{UID: 7777}) {
		t.Errorf("what the appliance said in no word of this package's is known as\n%s", shown(state))
	}

	if _, err := homeconnect.Open(t.Context(), fake.Addr(), fake.Key(), nil); !errors.Is(err, homeconnect.ErrArgument) {
		t.Errorf("Open with no profile = %v, want ErrArgument", err)
	}

	if _, err := homeconnect.Open(
		t.Context(),
		fake.Addr(),
		homeconnect.Key{},
		described(t),
	); !errors.Is(
		err,
		homeconnect.ErrArgument,
	) {
		t.Errorf("Open with no key = %v, want ErrArgument", err)
	}

	refusing := started(t)
	refusing.Refuse("/ro/allMandatoryValues", 500)

	if _, err := homeconnect.Open(
		t.Context(),
		refusing.Addr(),
		refusing.Key(),
		described(t),
	); !errors.Is(
		err,
		homeconnect.ErrRefused,
	) {
		t.Errorf("Open of an appliance that refuses its values = %v, want ErrRefused", err)
	}

	deadline := time.Now().Add(wait)
	for refusing.Connected() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	if refusing.Connected() != 0 {
		t.Error("an Open that failed left a session at the appliance")
	}
}

// shown is a state with what its parts point at, for a test's words.
func shown(state homeconnect.State) string {
	part := func(name string, held any) string {
		value := reflect.ValueOf(held)
		if value.IsNil() {
			return name + ": unknown\n"
		}

		return name + ": " + reflect.ValueOf(value.Elem().Interface()).String() + "\n"
	}

	return part("operation", state.Operation) + part("door", state.Door) + part("power", state.Power) +
		part(
			"remaining",
			state.Remaining,
		) + part("progress", state.Progress) + part("remote start", state.RemoteStart) +
		part("selected", state.Selected) + part("active", state.Active)
}

// TestNextChange: Next gives the state each time something of it changes,
// and at no other time: a value the profile does not name, and one said
// again as it was, are passed over. A word this package has not makes
// the part unknown, which is a change. The state handed out is the
// caller's own.
func TestNextChange(t *testing.T) {
	t.Parallel()

	fake := started(t)
	fake.Set(homeconnecttest.Operation, 1)
	fake.Set(homeconnecttest.Door, 1)

	appliance := opened(t, fake)
	ctx := soon(t)

	fake.Set(9999, 1)
	fake.Set(homeconnecttest.Door, 1)
	fake.Set(homeconnecttest.Door, 0)

	state, err := appliance.Next(ctx)
	if err != nil || state.Door == nil || *state.Door != homeconnect.DoorOpen || *state.Operation != homeconnect.Ready {
		t.Fatalf("Next after the door opened = %s, %v", shown(state), err)
	}

	*state.Door = homeconnect.DoorLocked

	if again := appliance.State(); *again.Door != homeconnect.DoorOpen {
		t.Error("Next handed out the appliance's own state")
	}

	for value, want := range map[int]homeconnect.Operation{
		3: homeconnect.Running, 4: homeconnect.Paused, 5: homeconnect.Waiting, 6: homeconnect.Finished,
		7: homeconnect.Failed, 8: homeconnect.Aborting, 2: homeconnect.Delayed, 0: homeconnect.Inactive,
	} {
		fake.Set(homeconnecttest.Operation, value)

		if state, err = appliance.Next(ctx); err != nil || state.Operation == nil || *state.Operation != want {
			t.Errorf("Next after the operation state became %d = %s, %v; want %s", value, shown(state), err, want)
		}
	}

	fake.Set(homeconnecttest.Operation, 99)

	if state, err = appliance.Next(ctx); err != nil || state.Operation != nil {
		t.Errorf("Next after an operation state this package has no word for = %s, %v", shown(state), err)
	}

	fake.Set(homeconnecttest.RemoteStart, true)
	fake.Set(homeconnecttest.Progress, 40)
	fake.Set(homeconnecttest.Remaining, 60)

	for range 3 {
		if state, err = appliance.Next(ctx); err != nil {
			t.Fatal(err)
		}
	}

	if state.RemoteStart == nil || !*state.RemoteStart || state.Progress == nil || *state.Progress != 40 ||
		state.Remaining == nil || *state.Remaining != time.Minute {
		t.Errorf("the state after three more changes is\n%s", shown(state))
	}

	// Values under another resource are no values of the state: what an
	// appliance says of a feature's access is passed over.
	fake.Say(homeconnect.Message{
		Resource: "/ro/descriptionChange", Version: 1, Action: homeconnect.Notify,
		Data: []byte(`[{"uid":527,"value":1,"access":"READ"}]`),
	})
	fake.Set(homeconnecttest.Progress, 41)

	if state, err = appliance.Next(ctx); err != nil || *state.Progress != 41 || *state.Door != homeconnect.DoorOpen {
		t.Errorf("Next after a description changed = %s, %v", shown(state), err)
	}

	// What cannot be read is the error, and the next call goes on.
	fake.SayText("not a message {")
	fake.Set(homeconnecttest.Door, 2)

	var unread *homeconnect.UnreadError
	if _, err = appliance.Next(ctx); !errors.As(err, &unread) {
		t.Errorf("Next of what is no message = %v, want an UnreadError", err)
	}

	if state, err = appliance.Next(ctx); err != nil || *state.Door != homeconnect.DoorLocked {
		t.Errorf("Next after what is no message = %s, %v", shown(state), err)
	}

	brief, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	if _, err = appliance.Next(brief); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Next with nothing changed = %v, want the context's end", err)
	}

	fake.Drop()

	if _, err = appliance.Next(ctx); !errors.Is(err, homeconnect.ErrLost) {
		t.Errorf("Next of an appliance that hung up = %v, want ErrLost", err)
	}

	select {
	case <-appliance.Done():
	case <-time.After(wait):
		t.Fatal("an appliance that hung up is not done")
	}

	if !errors.Is(appliance.Err(), homeconnect.ErrLost) {
		t.Errorf("Err of an appliance that hung up = %v", appliance.Err())
	}
}

// sent is the last message the fake received, once it has received more
// than before.
func sent(t *testing.T, fake *homeconnecttest.Appliance, before int) homeconnect.Message {
	t.Helper()

	deadline := time.Now().Add(wait)
	for len(fake.Received()) <= before {
		if time.Now().After(deadline) {
			t.Fatal("the appliance was sent nothing")
		}

		time.Sleep(2 * time.Millisecond)
	}

	received := fake.Received()

	return received[len(received)-1]
}

// TestCommands: each command is sent as the appliance takes it, by the
// id its profile gives the command's name; a start is the selected
// program's id, and the appliance then says it runs; power is the
// number of the member that is on, or off. With no program selected a
// start sends nothing; a refusal is the appliance's code; and what the
// profile has not is unsupported, with nothing sent.
func TestCommands(t *testing.T) {
	t.Parallel()

	fake := started(t)
	fake.Set(homeconnecttest.Operation, 1)

	appliance := opened(t, fake)
	ctx := soon(t)

	if err := appliance.Start(ctx); !errors.Is(err, homeconnect.ErrNoProgram) {
		t.Errorf("Start with no program selected = %v, want ErrNoProgram", err)
	}

	fake.Set(homeconnecttest.SelectedProgram, homeconnecttest.Auto)

	if _, err := appliance.Next(ctx); err != nil {
		t.Fatal(err)
	}

	for _, step := range []struct {
		name     string
		do       func() error
		resource string
		data     string
	}{
		{"pause", func() error { return appliance.Pause(ctx) }, "/ro/values", `[{"uid":560,"value":true}]`},
		{"resume", func() error { return appliance.Resume(ctx) }, "/ro/values", `[{"uid":561,"value":true}]`},
		{"stop", func() error { return appliance.Stop(ctx) }, "/ro/values", `[{"uid":512,"value":true}]`},
		{"power off", func() error { return appliance.SetPower(ctx, false) }, "/ro/values", `[{"uid":539,"value":1}]`},
		{"power on", func() error { return appliance.SetPower(ctx, true) }, "/ro/values", `[{"uid":539,"value":2}]`},
		{"start", func() error { return appliance.Start(ctx) }, "/ro/activeProgram", `[{"options":[],"program":8196}]`},
	} {
		before := len(fake.Received())

		if err := step.do(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}

		if got := sent(t, fake, before); got.Action != homeconnect.Post || got.Resource != step.resource ||
			string(got.Data) != step.data {
			t.Errorf("%s sent %s %s %s, want a POST of %s to %s", step.name, got.Action, got.Resource, got.Data,
				step.data, step.resource)
		}
	}

	// The appliance says what the start made of it, as any change.
	deadline := time.Now().Add(wait)

	for {
		state, err := appliance.Next(ctx)
		if err != nil || time.Now().After(deadline) {
			t.Fatalf("Next after a start: %v", err)
		}

		if state.Operation != nil && *state.Operation == homeconnect.Running && state.Active != nil &&
			state.Active.UID == homeconnecttest.Auto && state.Active.Name == "Dishcare.Dishwasher.Program.Auto2" {
			break
		}
	}

	fake.Refuse("/ro/activeProgram", 538)

	var refused *homeconnect.RefusedError
	if err := appliance.Start(ctx); !errors.As(err, &refused) || refused.Code != 538 {
		t.Errorf("a start the appliance refuses = %v, want its code", err)
	}

	// Off is off where the profile has it; an appliance with no such
	// member is turned off to standby.
	for members, want := range map[string]string{
		`<enumMember refValue="1">Off</enumMember><enumMember refValue="2">On</enumMember><enumMember refValue="3">Standby</enumMember>`: `[{"uid":539,"value":1}]`,
		`<enumMember refValue="2">On</enumMember><enumMember refValue="3">Standby</enumMember>`:                                          `[{"uid":539,"value":3}]`,
	} {
		powered, parseErr := profile.Parse(
			[]byte(
				`<device><settingList uid="0002"><setting access="readWrite" enumerationType="0300" uid="021B"/></settingList></device>`,
			),
			[]byte(
				`<featureMappingFile><featureDescription><feature refUID="021B">BSH.Common.Setting.PowerState</feature></featureDescription>`+
					`<enumDescriptionList><enumDescription refENID="0300">`+members+`</enumDescription></enumDescriptionList></featureMappingFile>`,
			),
		)
		if parseErr != nil {
			t.Fatal(parseErr)
		}

		other, openErr := homeconnect.Open(ctx, fake.Addr(), fake.Key(), powered)
		if openErr != nil {
			t.Fatal(openErr)
		}

		before := len(fake.Received())

		if offErr := other.SetPower(ctx, false); offErr != nil {
			t.Fatalf("SetPower(false): %v", offErr)
		}

		if got := sent(t, fake, before); string(got.Data) != want {
			t.Errorf("turning off sent %s, want %s", got.Data, want)
		}

		_ = other.Close()
	}

	bare, err := profile.Parse([]byte("<device/>"), []byte("<featureMappingFile/>"))
	if err != nil {
		t.Fatal(err)
	}

	plain, err := homeconnect.Open(ctx, fake.Addr(), fake.Key(), bare)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = plain.Close() })

	before := len(fake.Received())

	for name, do := range map[string]func() error{
		"pause":  func() error { return plain.Pause(ctx) },
		"resume": func() error { return plain.Resume(ctx) },
		"stop":   func() error { return plain.Stop(ctx) },
		"power":  func() error { return plain.SetPower(ctx, true) },
	} {
		if err = do(); !errors.Is(err, homeconnect.ErrUnsupported) {
			t.Errorf("%s on an appliance whose profile has none = %v, want ErrUnsupported", name, err)
		}
	}

	if got := fake.Received()[before:]; len(got) != 0 {
		t.Errorf("an appliance whose profile has no command was sent %+v", got)
	}
}

// TestACommandBesideANext: one goroutine waits in Next while others
// command; a second Next beside it ends with its own context.
func TestACommandBesideANext(t *testing.T) {
	t.Parallel()

	fake := started(t)
	fake.Set(homeconnecttest.SelectedProgram, homeconnecttest.Eco)

	appliance := opened(t, fake)
	ctx := soon(t)

	running := make(chan error, 1)

	go func() {
		for {
			state, err := appliance.Next(ctx)
			if err != nil {
				running <- err

				return
			}

			if state.Operation != nil && *state.Operation == homeconnect.Running {
				running <- nil

				return
			}
		}
	}()

	time.Sleep(50 * time.Millisecond)

	brief, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	if _, err := appliance.Next(brief); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a second Next with nothing changed = %v, want its context's end", err)
	}

	var commanding sync.WaitGroup

	for _, do := range []func(context.Context) error{appliance.Pause, appliance.Resume, appliance.Stop, appliance.Start} {
		commanding.Go(func() {
			if err := do(ctx); err != nil {
				t.Errorf("a command beside a Next: %v", err)
			}

			_ = appliance.State()
		})
	}

	commanding.Wait()

	if err := <-running; err != nil {
		t.Errorf("the reader beside the commands: %v", err)
	}
}
