package homeconnect

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/duncanite/go-bosch/internal/aeschain"
)

// Port is where an appliance of the AES mode listens.
const Port = "80"

const (
	// endpoint is the path of an appliance's WebSocket.
	endpoint = "/homeconnect"
	// heartbeat is how often an appliance is pinged when a Dialer does
	// not say.
	heartbeat = 20 * time.Second
	// largest bounds one frame. An appliance's longest answers, its
	// descriptions, are tens of kilobytes.
	largest = 4 << 20
	// queued bounds what an appliance said that no one has read yet.
	queued = 1024
	// The name and the id this module gives an appliance for itself when
	// a Dialer has none.
	clientName = "go-bosch"
	clientID   = "676f2d626f736368"
)

// The resources of the handshake.
const (
	resourceInitial  = "/ei/initialValues"
	resourceServices = "/ci/services"
	resourceAuth     = "/ci/authentication"
	resourceInfo     = "/ci/info"
	resourceIdentity = "/iz/info"
	resourceReady    = "/ei/deviceReady"
	resourceNetwork  = "/ni/info"
)

var (
	// ErrKey is when what answers at the address does not speak under this key.
	// The key is another appliance's, or what answers is no appliance.
	ErrKey = errors.New("homeconnect: the key does not open what answers there")
	// ErrLost is when the connection ended, and not by Close.
	ErrLost = errors.New("homeconnect: the connection to the appliance is lost")
	// ErrClosed is when the session was closed.
	ErrClosed = errors.New("homeconnect: the session is closed")
	// ErrAnswer is when the appliance said what this module cannot read.
	ErrAnswer = errors.New("homeconnect: the appliance's message cannot be read")
	// ErrRefused is when the appliance answered a request with a code. The error
	// is a *RefusedError, which carries it.
	ErrRefused = errors.New("homeconnect: the appliance refused")
	// ErrUnsupported is when the appliance speaks what this module does not.
	ErrUnsupported = errors.New("homeconnect: not supported")
	// ErrOverrun is when the appliance said more than was read, and the session
	// was ended in place of losing a message without a word.
	ErrOverrun = errors.New("homeconnect: the appliance's messages were not read")
)

// UnreadError is a message that came whole and authentic and that this
// module cannot read: it carries the message as the appliance wrote it.
// It is ErrAnswer. The session goes on after it.
type UnreadError struct {
	// Message is the message's text.
	Message []byte
	cause   error
}

func (e *UnreadError) Error() string { return e.cause.Error() }

// Unwrap gives why it could not be read, which is ErrAnswer.
func (e *UnreadError) Unwrap() error { return e.cause }

// Dialer is how a session is opened. The zero Dialer is the one Dial uses.
type Dialer struct {
	// Name and ID are what the client calls itself to the appliance.
	// Empty are this module's own, the same on every connection.
	Name string
	ID   string
	// Heartbeat is how often the appliance is pinged. One that says
	// nothing for twice that long, not even an answer to a ping, is
	// lost. Zero is twenty seconds, as the reference implementation has
	// it.
	Heartbeat time.Duration
}

// said is one thing an appliance said that was not the answer to a
// request: a message, or one that could not be read.
type said struct {
	message Message
	err     error
}

// Session is an open, encrypted session with an appliance. Its methods
// may be called from several goroutines.
type Session struct {
	conn  *websocket.Conn
	seal  *aeschain.Chain
	open  *aeschain.Chain
	beats time.Duration

	// writing holds the order of what is sent: a frame is sealed after
	// the one before it, and has to reach the wire in that order.
	writing sync.Mutex

	mu       sync.Mutex
	session  int64
	next     int64
	services map[string]int
	identity json.RawMessage
	pending  map[int64]chan Message
	heard    []said
	more     chan struct{}
	err      error

	done  chan struct{}
	ended sync.WaitGroup
}

// Dial opens a session with the appliance at an address, under the key of
// its profile: Dialer{}.Dial.
func Dial(ctx context.Context, address string, key Key) (*Session, error) {
	return Dialer{}.Dial(ctx, address, key)
}

// Dial opens a session with the appliance at an address, a host with or
// without a port, under the key of its profile. It returns once the
// appliance has opened the session and the handshake is done, within ctx.
//
// It is ErrKey when what answers does not speak under the key, and
// ErrUnsupported for the key of an appliance of the TLS mode, which this
// module does not speak.
func (d Dialer) Dial(ctx context.Context, address string, key Key) (*Session, error) {
	if len(key.psk) == 0 {
		return nil, fmt.Errorf("%w: Dial needs a key", ErrArgument)
	}

	if len(key.vector) == 0 {
		return nil, fmt.Errorf("%w: a key with no initial vector is an appliance's of the TLS mode", ErrUnsupported)
	}

	seal, err := aeschain.Sealer(key.psk, key.vector, aeschain.FromClient)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrArgument, err)
	}

	open, err := aeschain.Opener(key.psk, key.vector, aeschain.FromAppliance)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrArgument, err)
	}

	place := address
	if _, _, err = net.SplitHostPort(address); err != nil {
		place = net.JoinHostPort(strings.Trim(address, "[]"), Port)
	}

	conn, err := connect(ctx, place)
	if err != nil {
		return nil, err
	}

	session := &Session{
		conn: conn, seal: seal, open: open, beats: d.Heartbeat,
		services: map[string]int{}, pending: map[int64]chan Message{},
		more: make(chan struct{}, 1), done: make(chan struct{}),
	}

	if session.beats <= 0 {
		session.beats = heartbeat
	}

	conn.SetReadLimit(largest)
	session.alive()
	conn.SetPongHandler(func(string) error {
		session.alive()

		return nil
	})

	session.ended.Add(2)

	go session.read()
	go session.beat()

	if err = session.greet(ctx, d); err != nil {
		// What ended the session says more than the wait that it cut
		// short: a key that opens nothing, above all.
		if ended := session.Err(); ended != nil {
			err = ended
		}

		session.fail(ErrClosed)
		session.ended.Wait()

		return nil, fmt.Errorf("opening a session with the appliance at %s: %w", place, err)
	}

	return session, nil
}

// connect opens the WebSocket at a host and port.
func connect(ctx context.Context, place string) (*websocket.Conn, error) {
	host, port, err := net.SplitHostPort(place)
	if err != nil {
		return nil, fmt.Errorf("%w: the address %q: %w", ErrArgument, place, err)
	}

	// A zone names an interface of this host, and is no part of what the
	// appliance is asked for.
	named, _, _ := strings.Cut(host, "%")
	target := url.URL{Scheme: "ws", Host: net.JoinHostPort(named, port), Path: endpoint}

	dialer := websocket.Dialer{
		NetDialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return new(net.Dialer).DialContext(ctx, network, place)
		},
	}

	conn, response, err := dialer.DialContext(ctx, target.String(), nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if err != nil {
		return nil, fmt.Errorf("connecting to the appliance at %s: %w", place, err)
	}

	return conn, nil
}

// Get asks the appliance for a resource and gives its answer. Data is
// what the question carries, anything JSON writes, or nil. An answer with
// a code is a *RefusedError, and comes with the answer.
func (s *Session) Get(ctx context.Context, resource string, data any) (Message, error) {
	return s.request(ctx, Get, resource, data)
}

// Post sends data to a resource and gives the appliance's answer, as Get
// does.
func (s *Session) Post(ctx context.Context, resource string, data any) (Message, error) {
	return s.request(ctx, Post, resource, data)
}

// Notify tells the appliance of a resource, with data or nil, and waits
// for no answer: there is none.
func (s *Session) Notify(ctx context.Context, resource string, data any) error {
	if resource == "" {
		return fmt.Errorf("%w: a notification needs a resource", ErrArgument)
	}

	content, err := listed(data)
	if err != nil {
		return err
	}

	_, err = s.send(ctx, Message{Action: Notify, Resource: resource, Data: content}, nil)

	return err
}

// Next gives the next thing the appliance said that answered no request:
// what it notifies of, a value that changed above all, and the answer to
// a request whose caller waited no more, which is a Response. It waits for one,
// within ctx. A message that cannot be read comes as an *UnreadError, and
// the one after it by the next call; once the session has ended and all
// that was said is read, the error is what ended it.
func (s *Session) Next(ctx context.Context) (Message, error) {
	for {
		s.mu.Lock()

		if len(s.heard) > 0 {
			one := s.heard[0]
			s.heard = s.heard[1:]
			s.mu.Unlock()

			return one.message, one.err
		}

		ended := s.err
		s.mu.Unlock()

		if ended != nil {
			return Message{}, ended
		}

		select {
		case <-s.more:
		case <-s.done:
		case <-ctx.Done():
			return Message{}, fmt.Errorf("waiting for the appliance: %w", context.Cause(ctx))
		}
	}
}

// Services is the services the appliance said it has, each with its
// version.
func (s *Session) Services() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return maps.Clone(s.services)
}

// Identity is what the appliance said of itself in the handshake, as it
// wrote it: a JSON object with its brand, its model and its versions, and
// with its serial and its hardware address, which are not to be shown
// lightly. Nil when it said nothing.
func (s *Session) Identity() json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append(json.RawMessage(nil), s.identity...)
}

// Close ends the session. What a request in flight returns is ErrClosed.
func (s *Session) Close() error {
	const patience = time.Second

	_ = s.conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		time.Now().Add(patience),
	)

	s.fail(ErrClosed)
	s.ended.Wait()

	return nil
}

// Done is closed when the session has ended, by Close or otherwise.
func (s *Session) Done() <-chan struct{} { return s.done }

// Err is why the session ended: ErrClosed after Close, and nil while it
// stands.
func (s *Session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.err
}

// alive puts off the moment the appliance is taken for lost.
func (s *Session) alive() {
	const silences = 2

	_ = s.conn.SetReadDeadline(time.Now().Add(silences * s.beats))
}

// read takes every frame the appliance sends, in order, until the
// connection ends.
func (s *Session) read() {
	defer s.ended.Done()

	opened := false

	for {
		kind, frame, err := s.conn.ReadMessage()
		if err != nil {
			s.fail(fmt.Errorf("%w: %w", ErrLost, err))

			return
		}

		s.alive()

		if kind != websocket.BinaryMessage {
			s.fail(fmt.Errorf("%w: a frame that is not binary", ErrAnswer))

			return
		}

		text, err := s.open.Open(frame)
		if err != nil {
			// The first frame that does not authenticate is the key's
			// doing. A later one is a frame altered, foreign, or come
			// in the place of one that is missing, and which of them
			// cannot be told: after a missing one nothing opens.
			if errors.Is(err, aeschain.ErrTag) && !opened {
				err = ErrKey
			} else {
				err = fmt.Errorf("%w: %w", ErrLost, err)
			}

			s.fail(err)

			return
		}

		opened = true

		s.take(text)
	}
}

// take reads one message's text, and hands it on: to the request it
// answers, or to Next, which is told too of one that cannot be read.
func (s *Session) take(text []byte) {
	var message Message

	if err := json.Unmarshal(text, &message); err != nil {
		if !errors.Is(err, ErrAnswer) {
			err = fmt.Errorf("%w: %w", ErrAnswer, err)
		}

		s.tell(said{err: &UnreadError{Message: text, cause: err}})

		return
	}

	if message.Action == Response {
		s.mu.Lock()
		waiting, asked := s.pending[message.ID]
		delete(s.pending, message.ID)
		s.mu.Unlock()

		if asked {
			waiting <- message

			return
		}
	}

	s.tell(said{message: message})
}

// tell keeps what the appliance said for Next. More kept than queued ends
// the session: a message is not dropped without a word.
func (s *Session) tell(one said) {
	s.mu.Lock()

	if len(s.heard) >= queued {
		s.mu.Unlock()
		s.fail(ErrOverrun)

		return
	}

	s.heard = append(s.heard, one)
	s.mu.Unlock()

	select {
	case s.more <- struct{}{}:
	default:
	}
}

// beat pings the appliance until the session ends.
func (s *Session) beat() {
	defer s.ended.Done()

	ticker := time.NewTicker(s.beats)
	defer ticker.Stop()

	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			if err := s.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(s.beats)); err != nil {
				s.fail(fmt.Errorf("%w: %w", ErrLost, err))

				return
			}
		}
	}
}

// fail ends the session with its reason, once.
func (s *Session) fail(reason error) {
	s.mu.Lock()

	if s.err != nil {
		s.mu.Unlock()

		return
	}

	s.err = reason
	close(s.done)
	s.mu.Unlock()

	_ = s.conn.Close()
}

// greeting is how a client names itself to an appliance. The order of its
// keys is the protocol's: some firmware reads them by place.
type greeting struct {
	DeviceType any    `json:"deviceType"`
	DeviceName string `json:"deviceName"`
	DeviceID   string `json:"deviceID"` //nolint:tagliatelle // the protocol's key
}

// greet is the handshake: the appliance opens the session, the client
// names itself, asks which services there are, and answers for each as
// its version wants.
func (s *Session) greet(ctx context.Context, dialer Dialer) error {
	first, err := s.Next(ctx)
	if err != nil {
		return err
	}

	var initial []struct {
		Next number `json:"edMsgID"` //nolint:tagliatelle // the protocol's key
	}

	if first.Resource != resourceInitial || json.Unmarshal(first.Data, &initial) != nil || len(initial) == 0 {
		return fmt.Errorf("%w: its first message, about %q, does not open a session", ErrAnswer, first.Resource)
	}

	s.mu.Lock()
	s.session, s.next = first.Session, int64(initial[0].Next)
	s.mu.Unlock()

	// Version 1 of the service names a client's kind by a number.
	hello := greeting{DeviceType: "Application", DeviceName: dialer.Name, DeviceID: dialer.ID}

	if first.Version == 1 {
		const application = 2

		hello.DeviceType = application
	}

	if hello.DeviceName == "" {
		hello.DeviceName = clientName
	}

	if hello.DeviceID == "" {
		hello.DeviceID = clientID
	}

	answer := Message{
		Session:  first.Session,
		ID:       first.ID,
		Resource: first.Resource,
		Version:  first.Version,
		Action:   Response,
	}
	if answer.Data, err = listed(hello); err != nil {
		return err
	}

	if _, err = s.send(ctx, answer, nil); err != nil {
		return err
	}

	return s.introduce(ctx)
}

// introduce is the rest of the handshake, once the session has its
// numbers.
func (s *Session) introduce(ctx context.Context) error {
	answer, err := s.ask(ctx, Message{Action: Get, Resource: resourceServices, Version: 1})
	if err != nil {
		return err
	}

	var offered []struct {
		Service string `json:"service"`
		Version number `json:"version"`
	}

	if err = json.Unmarshal(answer.Data, &offered); err != nil {
		return fmt.Errorf("%w: its services: %w", ErrAnswer, err)
	}

	services := make(map[string]int, len(offered))
	for _, one := range offered {
		services[one.Service] = int(one.Version)
	}

	s.mu.Lock()
	s.services = services
	s.mu.Unlock()

	const authenticated = 3

	if version, known := services["ci"]; !known || version < authenticated {
		if err = s.authenticate(ctx); err != nil {
			return err
		}
	}

	return s.settle(ctx, services)
}

// settle is the end of the handshake: what the services beside the
// command's want, each when the appliance has it.
func (s *Session) settle(ctx context.Context, services map[string]int) error {
	const ready = 2

	if _, has := services["iz"]; has {
		answer, err := s.Get(ctx, resourceIdentity, nil)
		if err != nil {
			return err
		}

		s.identify(answer)
	}

	if services["ei"] == ready {
		if err := s.Notify(ctx, resourceReady, nil); err != nil {
			return err
		}
	}

	if _, has := services["ni"]; has {
		// Some appliances refuse it, and the session stands all the same.
		if _, err := s.Get(ctx, resourceNetwork, nil); err != nil && !errors.Is(err, ErrRefused) {
			return err
		}
	}

	return nil
}

// authenticate is what the older command service wants before anything
// else: a nonce, which the appliance answers with one of its own, and a
// question about itself that some refuse.
func (s *Session) authenticate(ctx context.Context) error {
	const nonceSize = 32

	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("making a nonce: %w", err)
	}

	token := map[string]string{"nonce": base64.RawURLEncoding.EncodeToString(nonce)}
	if _, err := s.Get(ctx, resourceAuth, token); err != nil {
		return err
	}

	answer, err := s.Get(ctx, resourceInfo, nil)

	switch {
	case err == nil:
		s.identify(answer)
	case !errors.Is(err, ErrRefused):
		return err
	}

	return nil
}

// identify keeps what the appliance first said of itself.
func (s *Session) identify(answer Message) {
	var objects []json.RawMessage

	if json.Unmarshal(answer.Data, &objects) != nil || len(objects) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.identity == nil {
		s.identity = objects[0]
	}
}

// send seals a message and writes it, in turn, and gives the id it went
// under. With waiting, the message is a request of this client's, and its
// answer goes to waiting, which the caller forgets when it waits no more.
func (s *Session) send(ctx context.Context, message Message, waiting chan Message) (int64, error) {
	s.writing.Lock()
	defer s.writing.Unlock()

	// Before an id is taken and anything is sealed: a request not sent
	// leaves no gap in the ids, and no frame the appliance never gets,
	// after which it could open none.
	if cause := context.Cause(ctx); cause != nil {
		return 0, fmt.Errorf("writing to the appliance: %w", cause)
	}

	s.mu.Lock()

	if s.err != nil {
		defer s.mu.Unlock()

		return 0, s.err
	}

	if message.Action != Response {
		message.Session, message.ID = s.session, s.next
		s.next++

		if message.Version == 0 {
			message.Version = s.version(message.Resource)
		}
	}

	if waiting != nil {
		s.pending[message.ID] = waiting
	}

	s.mu.Unlock()

	if err := s.write(ctx, message); err != nil {
		s.forget(message.ID)

		return 0, err
	}

	return message.ID, nil
}

// forget takes back the wait for an answer: the request was not sent, or
// its caller waits no more. An answer that comes all the same goes to
// Next.
func (s *Session) forget(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.pending, id)
}

// write seals one message and puts it on the wire. The caller holds the
// turn to write. A write that fails ends the session: part of a frame may
// be on the wire, and the chain has moved on.
func (s *Session) write(ctx context.Context, message Message) error {
	text, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrArgument, err)
	}

	frame, err := s.seal.Seal(text)
	if err != nil {
		s.fail(fmt.Errorf("%w: %w", ErrLost, err))

		return s.Err()
	}

	// An appliance that stops reading would hold the write, and the turn
	// with it, for good: the context's end is a deadline already past.
	fired := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = s.conn.NetConn().SetWriteDeadline(time.Now())

		close(fired)
	})

	err = s.conn.WriteMessage(websocket.BinaryMessage, frame)

	// The deadline is this write's alone: when it was set, it is taken
	// off again before the next writer, a ping among them, meets it.
	if !stop() {
		<-fired

		_ = s.conn.NetConn().SetWriteDeadline(time.Time{})
	}

	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			err = cause
		}

		s.fail(fmt.Errorf("%w: writing to the appliance: %w", ErrLost, err))

		return s.Err()
	}

	return nil
}

// version is the version of the service a resource belongs to, which is
// named by the resource's first part; one when the appliance did not say.
func (s *Session) version(resource string) int {
	service, _, _ := strings.Cut(strings.TrimPrefix(resource, "/"), "/")

	if version, known := s.services[service]; known && version > 0 {
		return version
	}

	return 1
}

// ask sends a request and waits for its answer.
func (s *Session) ask(ctx context.Context, request Message) (Message, error) {
	waiting := make(chan Message, 1)

	sent, err := s.send(ctx, request, waiting)
	if err != nil {
		return Message{}, err
	}

	select {
	case answer := <-waiting:
		const ok, above = 200, 300

		if answer.Code != 0 && (answer.Code < ok || answer.Code >= above) {
			return answer, &RefusedError{Code: answer.Code, Resource: request.Resource}
		}

		return answer, nil
	case <-s.done:
		return Message{}, s.Err()
	case <-ctx.Done():
		s.forget(sent)

		return Message{}, fmt.Errorf("waiting for the appliance's answer about %s: %w",
			request.Resource, context.Cause(ctx))
	}
}

// request is a Get or a Post with its data.
func (s *Session) request(ctx context.Context, action Action, resource string, data any) (Message, error) {
	if resource == "" {
		return Message{}, fmt.Errorf("%w: a request needs a resource", ErrArgument)
	}

	content, err := listed(data)
	if err != nil {
		return Message{}, err
	}

	return s.ask(ctx, Message{Action: action, Resource: resource, Data: content})
}
