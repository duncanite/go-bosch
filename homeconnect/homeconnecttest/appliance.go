// Package homeconnecttest is a Home Connect appliance for tests: the
// WebSocket of the AES mode on a loopback port, under a key made for
// tests, which opens sessions, answers the handshake, holds values, and
// does on demand what an appliance does wrong.
package homeconnecttest

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"maps"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/duncanite/go-bosch/homeconnect"
	"github.com/duncanite/go-bosch/internal/aeschain"
)

// The key and the initial vector an Appliance takes, as a profile writes
// them. Made up: they open nothing but an Appliance.
const (
	Key    = "dGhlIHRlc3QgYXBwbGlhbmNlJ3Mga2V5LCAzMiBieXQ"
	Vector = "YSB2ZWN0b3IsIDE2IGIuLg"
)

// What an Appliance writes in its messages.
const (
	// FirstID is the id an Appliance gives a client for its first
	// message.
	FirstID = 20
	// NotFound is the code an Appliance answers what it does not have
	// with.
	NotFound = 404
)

// The resources an Appliance answers.
const (
	initial      = "/ei/initialValues"
	services     = "/ci/services"
	authenticate = "/ci/authentication"
	info         = "/ci/info"
	identity     = "/iz/info"
	network      = "/ni/info"
	mandatory    = "/ro/allMandatoryValues"
	descriptions = "/ro/allDescriptionChanges"
	values       = "/ro/values"
	started      = "/ro/activeProgram"
)

// The ids of the features the test appliance's profile describes: a
// dishwasher's, under the names BSH gives them.
const (
	ActiveProgram   = 256
	SelectedProgram = 257
	Abort           = 512
	RemoteStart     = 523
	Door            = 527
	Power           = 539
	Progress        = 542
	Remaining       = 544
	Operation       = 552
	Pause           = 560
	Resume          = 561
	Eco             = 8195
	Auto            = 8196
)

// The test appliance's profile, as its two documents.
const (
	description = `<device>
  <statusList access="read" uid="0001">
    <status access="read" refCID="01" uid="020B"/>
    <status access="read" enumerationType="0201" refCID="03" uid="020F"/>
    <status access="read" enumerationType="0228" refCID="03" uid="0228"/>
  </statusList>
  <settingList access="readWrite" uid="0002">
    <setting access="readWrite" enumerationType="0301" refCID="03" uid="021B"/>
  </settingList>
  <optionList access="read" uid="0003">
    <option access="read" refCID="11" uid="021E"/>
    <option access="read" refCID="10" uid="0220"/>
  </optionList>
  <commandList access="writeOnly" uid="0004">
    <command access="writeOnly" refCID="01" uid="0200"/>
    <command access="writeOnly" refCID="01" uid="0230"/>
    <command access="writeOnly" refCID="01" uid="0231"/>
  </commandList>
  <programGroup uid="0005">
    <program execution="selectAndStart" uid="2003"/>
    <program execution="selectAndStart" uid="2004"/>
  </programGroup>
  <activeProgram access="readWrite" uid="0100"/>
  <selectedProgram access="readWrite" uid="0101"/>
  <enumerationTypeList>
    <enumerationType enid="0301" subsetOf="0300"><enumeration value="1"/><enumeration value="2"/></enumerationType>
  </enumerationTypeList>
</device>`

	mapping = `<featureMappingFile>
  <featureDescription>
    <feature refUID="020B">BSH.Common.Status.RemoteControlStartAllowed</feature>
    <feature refUID="020F">BSH.Common.Status.DoorState</feature>
    <feature refUID="0228">BSH.Common.Status.OperationState</feature>
    <feature refUID="021B">BSH.Common.Setting.PowerState</feature>
    <feature refUID="021E">BSH.Common.Option.ProgramProgress</feature>
    <feature refUID="0220">BSH.Common.Option.RemainingProgramTime</feature>
    <feature refUID="0200">BSH.Common.Command.AbortProgram</feature>
    <feature refUID="0230">BSH.Common.Command.PauseProgram</feature>
    <feature refUID="0231">BSH.Common.Command.ResumeProgram</feature>
    <feature refUID="2003">Dishcare.Dishwasher.Program.Eco50</feature>
    <feature refUID="2004">Dishcare.Dishwasher.Program.Auto2</feature>
    <feature refUID="0100">BSH.Common.Root.ActiveProgram</feature>
    <feature refUID="0101">BSH.Common.Root.SelectedProgram</feature>
  </featureDescription>
  <enumDescriptionList>
    <enumDescription refENID="0201" enumKey="DoorState">
      <enumMember refValue="0">Open</enumMember>
      <enumMember refValue="1">Closed</enumMember>
      <enumMember refValue="2">Locked</enumMember>
    </enumDescription>
    <enumDescription refENID="0228" enumKey="OperationState">
      <enumMember refValue="0">Inactive</enumMember>
      <enumMember refValue="1">Ready</enumMember>
      <enumMember refValue="2">DelayedStart</enumMember>
      <enumMember refValue="3">Run</enumMember>
      <enumMember refValue="4">Pause</enumMember>
      <enumMember refValue="5">ActionRequired</enumMember>
      <enumMember refValue="6">Finished</enumMember>
      <enumMember refValue="7">Error</enumMember>
      <enumMember refValue="8">Aborting</enumMember>
    </enumDescription>
    <enumDescription refENID="0300" enumKey="PowerState">
      <enumMember refValue="1">BSH.Common.EnumType.PowerState.Off</enumMember>
      <enumMember refValue="2">BSH.Common.EnumType.PowerState.On</enumMember>
      <enumMember refValue="3">BSH.Common.EnumType.PowerState.Standby</enumMember>
    </enumDescription>
  </enumDescriptionList>
</featureMappingFile>`
)

// Profile is the archive of the test appliance's profile, as an account
// gives one: what package profile reads, and what names the ids above.
func Profile() []byte {
	var archive bytes.Buffer

	writer := zip.NewWriter(&archive)

	for _, file := range [][2]string{
		{"TEST-DISHWASHER-000000000001_DeviceDescription.xml", description},
		{"TEST-DISHWASHER-000000000001_FeatureMapping.xml", mapping},
	} {
		entry, err := writer.Create(file[0])
		if err != nil {
			panic(err)
		}

		if _, err = entry.Write([]byte(file[1])); err != nil {
			panic(err)
		}
	}

	if err := writer.Close(); err != nil {
		panic(err)
	}

	return archive.Bytes()
}

// client is one session with an Appliance.
type client struct {
	conn    *websocket.Conn
	session int64

	mu   sync.Mutex
	seal *aeschain.Chain
}

// say seals a text and sends it.
func (c *client) say(text []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	frame, err := c.seal.Seal(text)
	if err != nil {
		return
	}

	_ = c.conn.WriteMessage(websocket.BinaryMessage, frame)
}

// Appliance is the fake, listening until Close.
type Appliance struct {
	listener net.Listener
	server   *http.Server
	psk      []byte
	vector   []byte

	mu       sync.Mutex
	offered  map[string]int
	held     map[int]any
	refused  map[string]int
	stalled  map[string]bool
	itself   map[string]any
	received []homeconnect.Message
	greeted  []string
	clients  []*client
	sessions int64
	deaf     bool
}

// New starts an appliance on a loopback port: a dishwasher with the
// services a session's handshake asks about, each at the version that
// has a client do every step.
func New() *Appliance {
	psk, err := base64.RawURLEncoding.DecodeString(Key)
	if err != nil {
		panic(err)
	}

	vector, err := base64.RawURLEncoding.DecodeString(Vector)
	if err != nil {
		panic(err)
	}

	listener, err := new(net.ListenConfig).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}

	appliance := &Appliance{
		listener: listener,
		psk:      psk,
		vector:   vector,
		offered:  map[string]int{"ci": 2, "ei": 2, "iz": 1, "ni": 1, "ro": 1},
		held:     map[int]any{},
		refused:  map[string]int{},
		stalled:  map[string]bool{},
		itself: map[string]any{
			"brand": "THERMADOR", "vib": "DWHD000XXX", "eNumber": "DWHD000XXX/01", "deviceInfo": "DISHWASHER",
			"haVersion": "1.0", "swVersion": "2.17.1", "hwVersion": "0000000001",
			"deviceID": "THERMADOR-DWHD000XXX-000000000001", "mac": "00-00-00-00-00-01",
			"serialNumber": "000000000000000001",
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/homeconnect", appliance.session)

	const patience = 5 * time.Second

	appliance.server = &http.Server{Handler: mux, ReadHeaderTimeout: patience}

	go func() { _ = appliance.server.Serve(listener) }()

	return appliance
}

// Addr is the appliance's address, host and port.
func (a *Appliance) Addr() string { return a.listener.Addr().String() }

// Key is the key that opens the appliance.
func (*Appliance) Key() homeconnect.Key {
	key, err := homeconnect.ParseKey(Key, Vector)
	if err != nil {
		panic(err)
	}

	return key
}

// Serve has the appliance offer a service at a version from now on, or
// offer it no more when the version is zero.
func (a *Appliance) Serve(service string, version int) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if version == 0 {
		delete(a.offered, service)

		return
	}

	a.offered[service] = version
}

// Set has the appliance hold a value under an id, and tell every client
// of it, as an appliance does when something changes.
func (a *Appliance) Set(uid int, value any) {
	a.mu.Lock()
	a.held[uid] = value
	a.mu.Unlock()

	a.Say(homeconnect.Message{
		Resource: values, Version: 1, Action: homeconnect.Notify,
		Data: written([]map[string]any{{"uid": uid, "value": value}}),
	})
}

// Refuse has the appliance answer every request about a resource with a
// code from now on; zero has it answer as before.
func (a *Appliance) Refuse(resource string, code int) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if code == 0 {
		delete(a.refused, resource)

		return
	}

	a.refused[resource] = code
}

// Stall has the appliance never answer a request about a resource.
func (a *Appliance) Stall(resource string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.stalled[resource] = true
}

// Deaf has the appliance answer nothing from now on, a ping included,
// while it keeps its connections open.
func (a *Appliance) Deaf() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.deaf = true
}

// Received is every message clients sent, in order.
func (a *Appliance) Received() []homeconnect.Message {
	a.mu.Lock()
	defer a.mu.Unlock()

	return slices.Clone(a.received)
}

// Greeted is the first message of each client, as it was written: how
// the client named itself.
func (a *Appliance) Greeted() []string {
	a.mu.Lock()
	defer a.mu.Unlock()

	return slices.Clone(a.greeted)
}

// Say sends a message to every client, under each one's session when the
// message names none.
func (a *Appliance) Say(message homeconnect.Message) {
	for _, one := range a.all() {
		told := message
		if told.Session == 0 {
			told.Session = one.session
		}

		one.say(written(told))
	}
}

// SayText sends every client a text, sealed as a message is, whatever it
// holds.
func (a *Appliance) SayText(text string) {
	for _, one := range a.all() {
		one.say([]byte(text))
	}
}

// Garble sends every client a frame that does not authenticate.
func (a *Appliance) Garble() {
	const blocks = 3

	for _, one := range a.all() {
		one.mu.Lock()
		_ = one.conn.WriteMessage(websocket.BinaryMessage, make([]byte, blocks*aeschain.TagSize))
		one.mu.Unlock()
	}
}

// Drop hangs up on every client.
func (a *Appliance) Drop() {
	for _, one := range a.all() {
		_ = one.conn.Close()
	}
}

// Connected is how many clients are connected.
func (a *Appliance) Connected() int { return len(a.all()) }

// Close stops the appliance and hangs up on its clients.
func (a *Appliance) Close() {
	_ = a.server.Close()

	a.Drop()
}

func (a *Appliance) all() []*client {
	a.mu.Lock()
	defer a.mu.Unlock()

	return slices.Clone(a.clients)
}

// written is a value as JSON.
func written(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}

	return raw
}

// session is one client's connection, from the upgrade to its end.
func (a *Appliance) session(writer http.ResponseWriter, request *http.Request) {
	conn, err := new(websocket.Upgrader).Upgrade(writer, request, nil)
	if err != nil {
		return
	}

	defer func() { _ = conn.Close() }()

	seal, err := aeschain.Sealer(a.psk, a.vector, aeschain.FromAppliance)
	if err != nil {
		panic(err)
	}

	open, err := aeschain.Opener(a.psk, a.vector, aeschain.FromClient)
	if err != nil {
		panic(err)
	}

	a.mu.Lock()
	a.sessions++
	one := &client{conn: conn, session: a.sessions, seal: seal}
	a.clients = append(a.clients, one)
	version := a.offered["ei"]
	a.mu.Unlock()

	defer a.leave(one)

	answering := conn.PingHandler()
	conn.SetPingHandler(func(data string) error {
		if a.isDeaf() {
			return nil
		}

		return answering(data)
	})

	const opening = 1000

	one.say(written(homeconnect.Message{
		Session: one.session, ID: opening, Resource: initial, Version: version, Action: homeconnect.Post,
		Data: written([]map[string]any{{"edMsgID": FirstID}}),
	}))

	for first := true; ; first = false {
		_, frame, readErr := conn.ReadMessage()
		if readErr != nil {
			return
		}

		text, openErr := open.Open(frame)
		if openErr != nil {
			return
		}

		var message homeconnect.Message
		if json.Unmarshal(text, &message) != nil {
			return
		}

		a.record(message, text, first)

		if !a.isDeaf() {
			a.answer(one, message)
		}
	}
}

// record keeps what a client sent, and how it first named itself.
func (a *Appliance) record(message homeconnect.Message, text []byte, first bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.received = append(a.received, message)

	if first {
		a.greeted = append(a.greeted, string(text))
	}
}

func (a *Appliance) isDeaf() bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	return a.deaf
}

func (a *Appliance) leave(gone *client) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.clients = slices.DeleteFunc(a.clients, func(one *client) bool { return one == gone })
}

// answer is what the appliance says to a request; nothing to what is no
// request.
func (a *Appliance) answer(from *client, request homeconnect.Message) {
	if request.Action != homeconnect.Get && request.Action != homeconnect.Post {
		return
	}

	answer := homeconnect.Message{
		Session: request.Session, ID: request.ID, Resource: request.Resource,
		Version: request.Version, Action: homeconnect.Response,
	}

	a.mu.Lock()
	code, refused := a.refused[request.Resource]
	stalled := a.stalled[request.Resource]

	switch {
	case stalled:
	case refused:
		answer.Code = code
	case request.Resource == services:
		offered := make([]map[string]any, 0, len(a.offered))
		for _, service := range slices.Sorted(maps.Keys(a.offered)) {
			offered = append(offered, map[string]any{"service": service, "version": a.offered[service]})
		}

		answer.Data = written(offered)
	case request.Resource == authenticate:
		answer.Data = written([]map[string]any{{"nonce": "bm9uY2U"}})
	case request.Resource == info, request.Resource == identity:
		answer.Data = written([]map[string]any{a.itself})
	case request.Resource == network:
		answer.Data = written([]map[string]any{{"interfaceID": 0, "primary": true}})
	case request.Resource == descriptions:
		answer.Data = written([]any{})
	case request.Resource == mandatory:
		held := make([]map[string]any, 0, len(a.held))
		for _, uid := range slices.Sorted(maps.Keys(a.held)) {
			held = append(held, map[string]any{"uid": uid, "value": a.held[uid]})
		}

		answer.Data = written(held)
	case request.Resource == values && request.Action == homeconnect.Post:
	case request.Resource == started && request.Action == homeconnect.Post:
	default:
		answer.Code = NotFound
	}

	a.mu.Unlock()

	if stalled {
		return
	}

	from.say(written(answer))

	if answer.Code != 0 || request.Action != homeconnect.Post {
		return
	}

	switch request.Resource {
	case values:
		a.take(request)
	case started:
		a.start(request)
	}
}

// start runs the program a client asked for: the appliance says it is
// the active one, and that it runs.
func (a *Appliance) start(request homeconnect.Message) {
	var asked []struct {
		Program int `json:"program"`
	}

	if json.Unmarshal(request.Data, &asked) != nil || len(asked) == 0 {
		return
	}

	const running = 3

	a.Set(ActiveProgram, asked[0].Program)
	a.Set(Operation, running)
}

// take holds the values a client sent, and tells every client of each.
func (a *Appliance) take(request homeconnect.Message) {
	var sent []struct {
		UID   int `json:"uid"`
		Value any `json:"value"`
	}

	if json.Unmarshal(request.Data, &sent) != nil {
		return
	}

	for _, one := range sent {
		a.Set(one.UID, one.Value)
	}
}
