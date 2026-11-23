package homeconnect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/duncanite/go-bosch/homeconnect/profile"
)

// The features an Appliance reads and drives, by the names BSH gives
// them in every appliance's profile.
const (
	featureOperation   = "BSH.Common.Status.OperationState"
	featureDoor        = "BSH.Common.Status.DoorState"
	featureRemoteStart = "BSH.Common.Status.RemoteControlStartAllowed"
	featureRemaining   = "BSH.Common.Option.RemainingProgramTime"
	featureProgress    = "BSH.Common.Option.ProgramProgress"
	featurePower       = "BSH.Common.Setting.PowerState"
	featureActive      = "BSH.Common.Root.ActiveProgram"
	featureSelected    = "BSH.Common.Root.SelectedProgram"
	commandPause       = "BSH.Common.Command.PauseProgram"
	commandResume      = "BSH.Common.Command.ResumeProgram"
	commandAbort       = "BSH.Common.Command.AbortProgram"
)

// The resources values are read and set at, and a program is started at.
const (
	resourceAll     = "/ro/allMandatoryValues"
	resourceValues  = "/ro/values"
	resourceStarted = "/ro/activeProgram"
)

// ErrNoProgram is a start with no program selected to start.
var ErrNoProgram = errors.New("homeconnect: no program is selected")

// Operation is what an appliance is doing.
type Operation string

// What an appliance may be doing, as BSH's operation state has it.
const (
	// Inactive is switched off, or on standby.
	Inactive Operation = "inactive"
	// Ready is switched on, with no program active.
	Ready Operation = "ready"
	// Delayed is a program set to start later.
	Delayed Operation = "delayed"
	// Running is a program under way.
	Running Operation = "running"
	// Paused is a program held.
	Paused Operation = "paused"
	// Waiting is a program that needs a person.
	Waiting Operation = "waiting"
	// Finished is a program ended.
	Finished Operation = "finished"
	// Failed is an appliance in an error state.
	Failed Operation = "failed"
	// Aborting is a program being stopped.
	Aborting Operation = "aborting"
)

// Door is how an appliance's door stands.
type Door string

// How a door may stand.
const (
	DoorOpen   Door = "open"
	DoorClosed Door = "closed"
	DoorLocked Door = "locked"
)

// Power is whether an appliance is on.
type Power string

// What an appliance's power may be set to.
const (
	PowerOff     Power = "off"
	PowerOn      Power = "on"
	PowerStandby Power = "standby"
)

// The members of BSH's enumerations, by the last part of their names,
// and what this package calls each.
//
//nolint:gochecknoglobals // tables, read only
var (
	operations = map[string]Operation{
		"Inactive": Inactive, "Ready": Ready, "DelayedStart": Delayed, "Run": Running, "Pause": Paused,
		"ActionRequired": Waiting, "Finished": Finished, "Error": Failed, "Aborting": Aborting,
	}
	doors  = map[string]Door{"Open": DoorOpen, "Closed": DoorClosed, "Locked": DoorLocked}
	powers = map[string]Power{"Off": PowerOff, "On": PowerOn, "Standby": PowerStandby}
)

// Program is a program of an appliance.
type Program struct {
	// UID is the id the appliance knows it by. Zero is no program.
	UID int
	// Name is what the appliance's profile calls it,
	// "Dishcare.Dishwasher.Program.Eco50": BSH's own name, the same on
	// every appliance that has the program. Empty when the profile names
	// the id nowhere.
	Name string
}

// State is what is known of an appliance. Each part is nil until the
// appliance has said it, and when it said what this package has no word
// for: a zero is never a reading.
type State struct {
	// Operation is what the appliance is doing.
	Operation *Operation
	// Door is how its door stands.
	Door *Door
	// Remaining is how long the program has left: the running one's, or
	// with none running the time the selected one would take, which an
	// appliance says all the same. Operation says whether one runs.
	Remaining *time.Duration
	// Progress is how far the running program is, in percent.
	Progress *int
	// Power is whether the appliance is on.
	Power *Power
	// RemoteStart is whether the appliance lets a program be started
	// from afar: a person allows it at the appliance, and opening the
	// door takes it back.
	RemoteStart *bool
	// Selected is the program that is selected, and Active the one that
	// runs; a Program with no id is none.
	Selected *Program
	Active   *Program
}

// Appliance is an appliance spoken to under its profile: what it is
// doing, told as it changes, and the commands it takes. Its methods may
// be called from several goroutines: one waits in Next while others
// command.
type Appliance struct {
	session   *Session
	described *profile.Profile

	mu    sync.Mutex
	state State
}

// Open opens a session with the appliance at an address, under its key,
// and reads what it holds, named by its profile: Dialer{}.Open.
func Open(ctx context.Context, address string, key Key, described *profile.Profile) (*Appliance, error) {
	return Dialer{}.Open(ctx, address, key, described)
}

// Open opens a session with the appliance at an address, under its key,
// and reads what it holds, named by its profile. The appliance's broker
// of sessions is small: Close the Appliance.
func (d Dialer) Open(ctx context.Context, address string, key Key, described *profile.Profile) (*Appliance, error) {
	if described == nil {
		return nil, fmt.Errorf("%w: Open needs the appliance's profile", ErrArgument)
	}

	session, err := d.Dial(ctx, address, key)
	if err != nil {
		return nil, err
	}

	appliance := &Appliance{session: session, described: described}

	answer, err := session.Get(ctx, resourceAll, nil)
	if err != nil {
		_ = session.Close()

		return nil, fmt.Errorf("reading what the appliance holds: %w", err)
	}

	appliance.take(answer.Data)

	return appliance, nil
}

// State is what is known of the appliance now.
func (a *Appliance) State() State {
	a.mu.Lock()
	defer a.mu.Unlock()

	return a.state.copied()
}

// copied is a state whose parts are the caller's own.
func (s *State) copied() State {
	return State{
		Operation: again(s.Operation), Door: again(s.Door), Remaining: again(s.Remaining), Progress: again(s.Progress),
		Power: again(s.Power), RemoteStart: again(s.RemoteStart), Selected: again(s.Selected), Active: again(s.Active),
	}
}

// again is a copy of what a pointer points at; nil of nil.
func again[T any](held *T) *T {
	if held == nil {
		return nil
	}

	value := *held

	return &value
}

// Next waits until something of the appliance's state changes, and gives
// the state as it then is. What the appliance says that changes nothing
// read here is passed over. A message that cannot be read is the error,
// an *UnreadError, and the next call goes on; when the session has ended
// the error is what ended it.
//
// A Next that ends with its context has taken nothing. Called from
// several goroutines, each thing the appliance says goes to one of them,
// and the state each is given holds all that was said by then.
func (a *Appliance) Next(ctx context.Context) (State, error) {
	for {
		message, err := a.session.Next(ctx)
		if err != nil {
			return State{}, err
		}

		if message.Resource != resourceValues && message.Resource != resourceAll {
			continue
		}

		if a.take(message.Data) {
			return a.State(), nil
		}
	}
}

// member is the name of the member a value is, by the last part of the
// name the profile gives it: a profile writes "Run" or the whole of
// "BSH.Common.EnumType.OperationState.Run".
func member(feature profile.Feature, raw json.RawMessage) string {
	name, _ := feature.Says(raw)

	if cut := strings.LastIndexByte(name, '.'); cut >= 0 {
		name = name[cut+1:]
	}

	return name
}

// put sets a part of a state, and says whether that changed it. With no
// word for what the appliance said, the part is unknown.
func put[T comparable](part **T, said T, known bool) bool {
	held := *part

	switch {
	case !known:
		*part = nil

		return held != nil
	case held != nil && *held == said:
		return false
	default:
		*part = &said

		return true
	}
}

// read takes one value into the state, by its feature's name, and says
// whether the state changed.
func (s *State) read(described *profile.Profile, feature profile.Feature, raw json.RawMessage) bool {
	switch feature.Name {
	case featureOperation:
		said, known := operations[member(feature, raw)]

		return put(&s.Operation, said, known)
	case featureDoor:
		said, known := doors[member(feature, raw)]

		return put(&s.Door, said, known)
	case featurePower:
		said, known := powers[member(feature, raw)]

		return put(&s.Power, said, known)
	case featureRemoteStart:
		var allowed bool

		err := json.Unmarshal(raw, &allowed)

		return put(&s.RemoteStart, allowed, err == nil)
	case featureRemaining:
		var seconds int

		err := json.Unmarshal(raw, &seconds)

		return put(&s.Remaining, time.Duration(seconds)*time.Second, err == nil && seconds >= 0)
	case featureProgress:
		var percent int

		err := json.Unmarshal(raw, &percent)

		return put(&s.Progress, percent, err == nil)
	case featureSelected:
		return put(&s.Selected, program(described, raw), true)
	case featureActive:
		return put(&s.Active, program(described, raw), true)
	default:
		return false
	}
}

// program is the program an id stands for, named when the profile names
// it; none when the value is no id.
func program(described *profile.Profile, raw json.RawMessage) Program {
	var uid int

	if json.Unmarshal(raw, &uid) != nil || uid <= 0 {
		return Program{}
	}

	named := Program{UID: uid}

	if feature, known := described.Feature(uid); known {
		named.Name = feature.Name
	}

	return named
}

// Pause holds the running program. Like every command, it returns when
// the appliance has taken the request; what the appliance then does comes
// as a change, which Next gives. An appliance whose profile has no such
// command is ErrUnsupported, and one that refuses is a *RefusedError.
func (a *Appliance) Pause(ctx context.Context) error { return a.command(ctx, commandPause) }

// Resume goes on with a paused program.
func (a *Appliance) Resume(ctx context.Context) error { return a.command(ctx, commandResume) }

// Stop ends the running program.
func (a *Appliance) Stop(ctx context.Context) error { return a.command(ctx, commandAbort) }

// Start starts the program that is selected: it sends that program's id
// and no option. Whether an appliance then runs it with the options
// chosen at its panel, or wants them sent, is not known; the reference
// implementation sends them when the appliance's description asks for
// the full set, which this package does not read. It is ErrNoProgram when
// no program is known to be selected. An appliance starts a program from
// afar only when a person has allowed it there, which State's RemoteStart
// says; otherwise it refuses.
func (a *Appliance) Start(ctx context.Context) error {
	selected := a.State().Selected
	if selected == nil || selected.UID == 0 {
		return ErrNoProgram
	}

	_, err := a.session.Post(ctx, resourceStarted, map[string]any{"program": selected.UID, "options": []any{}})

	return err
}

// SetPower turns the appliance on, or off: to off where its profile has
// that, and to standby where it has no other way of being off.
func (a *Appliance) SetPower(ctx context.Context, powered bool) error {
	uid, has := a.described.UID(featurePower)
	if !has {
		return fmt.Errorf("%w: the appliance's profile has no %s", ErrUnsupported, featurePower)
	}

	feature, _ := a.described.Feature(uid)
	wanted := []Power{PowerOff, PowerStandby}

	if powered {
		wanted = []Power{PowerOn}
	}

	for _, want := range wanted {
		for number, name := range feature.Values {
			if cut := strings.LastIndexByte(name, '.'); cut >= 0 {
				name = name[cut+1:]
			}

			if powers[name] == want {
				_, err := a.session.Post(ctx, resourceValues, map[string]any{"uid": uid, "value": number})

				return err
			}
		}
	}

	return fmt.Errorf("%w: the appliance's power cannot be set so", ErrUnsupported)
}

// Close ends the session.
func (a *Appliance) Close() error { return a.session.Close() }

// Done is closed when the session has ended, by Close or otherwise.
// Nothing connects again by itself: an appliance that went is the
// caller's to Open again.
func (a *Appliance) Done() <-chan struct{} { return a.session.Done() }

// Err is why the session ended: ErrClosed after Close, and nil while it
// stands.
func (a *Appliance) Err() error { return a.session.Err() }

// value is one value as an appliance writes it.
type value struct {
	UID   *int            `json:"uid"`
	Value json.RawMessage `json:"value"`
}

// take reads the values of a message into the state, and says whether
// any part of the state changed.
func (a *Appliance) take(data json.RawMessage) bool {
	var said []value

	if json.Unmarshal(data, &said) != nil {
		return false
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	changed := false

	for _, one := range said {
		if one.UID == nil || len(one.Value) == 0 {
			continue
		}

		feature, known := a.described.Feature(*one.UID)
		if !known {
			continue
		}

		if a.state.read(a.described, feature, one.Value) {
			changed = true
		}
	}

	return changed
}

// command has the appliance take a command of its profile, by name.
func (a *Appliance) command(ctx context.Context, name string) error {
	uid, has := a.described.UID(name)
	if !has {
		return fmt.Errorf("%w: the appliance's profile has no %s", ErrUnsupported, name)
	}

	_, err := a.session.Post(ctx, resourceValues, map[string]any{"uid": uid, "value": true})

	return err
}
