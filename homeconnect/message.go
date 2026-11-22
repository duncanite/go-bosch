package homeconnect

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// Action is what a message does.
type Action string

// The actions of the protocol.
const (
	// Get asks for a resource.
	Get Action = "GET"
	// Post sends to a resource: a value to set, a program to start.
	Post Action = "POST"
	// Response answers a Get or a Post, under the same id.
	Response Action = "RESPONSE"
	// Notify tells, and is not answered.
	Notify Action = "NOTIFY"
)

// Message is one message of the protocol, in either direction.
type Message struct {
	// Session is the session's id, which the appliance gives when it
	// opens one.
	Session int64
	// ID numbers the message; a Response carries the id of what it
	// answers.
	ID int64
	// Resource is what the message is about: "/ro/values", "/ci/info".
	Resource string
	// Version is the version of the service the resource belongs to.
	Version int
	// Action is what the message does.
	Action Action
	// Data is the message's content, a JSON list; nil when it has none.
	Data json.RawMessage
	// Code is how an appliance refuses in a Response; zero is no code.
	Code int
}

// errNumber is a number of a message that is none.
var errNumber = errors.New("not an integer")

// number is an integer of a message. Some firmware writes them as
// strings.
type number int64

func (n *number) UnmarshalJSON(raw []byte) error {
	parsed, err := strconv.ParseInt(string(bytes.Trim(raw, `"`)), 10, 64)
	if err != nil {
		return fmt.Errorf("%w: %s", errNumber, raw)
	}

	*n = number(parsed)

	return nil
}

// wire is a message as it is written. The keys are the protocol's.
type wire struct {
	Session  number          `json:"sID"`   //nolint:tagliatelle // the protocol's key
	ID       number          `json:"msgID"` //nolint:tagliatelle // the protocol's key
	Resource string          `json:"resource"`
	Version  number          `json:"version"`
	Action   Action          `json:"action"`
	Data     json.RawMessage `json:"data,omitempty"`
	Code     *number         `json:"code,omitempty"`
}

// MarshalJSON writes the message as the protocol does.
func (m Message) MarshalJSON() ([]byte, error) {
	written := wire{
		Session: number(m.Session), ID: number(m.ID), Resource: m.Resource,
		Version: number(m.Version), Action: m.Action, Data: m.Data,
	}

	if m.Code != 0 {
		code := number(m.Code)
		written.Code = &code
	}

	raw, err := json.Marshal(written)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrArgument, err)
	}

	return raw, nil
}

// UnmarshalJSON reads a message as an appliance writes it.
func (m *Message) UnmarshalJSON(raw []byte) error {
	var read wire

	if err := json.Unmarshal(raw, &read); err != nil {
		return fmt.Errorf("%w: %w", ErrAnswer, err)
	}

	if read.Resource == "" || read.Action == "" {
		return fmt.Errorf("%w: a message with no resource or no action", ErrAnswer)
	}

	*m = Message{
		Session: int64(read.Session), ID: int64(read.ID), Resource: read.Resource,
		Version: int(read.Version), Action: read.Action,
	}

	if data := bytes.TrimSpace(read.Data); len(data) > 0 && !bytes.Equal(data, []byte("null")) {
		m.Data = append(json.RawMessage(nil), data...)
	}

	if read.Code != nil {
		m.Code = int(*read.Code)
	}

	return nil
}

// RefusedError is an appliance's refusal: the code it answered a request
// with, and the resource asked. It is ErrRefused.
type RefusedError struct {
	Code     int
	Resource string
}

func (e *RefusedError) Error() string {
	name, known := codes[e.Code]
	if !known {
		name = "a code this module has no name for"
	}

	return fmt.Sprintf("homeconnect: the appliance refused %s: %d, %s", e.Resource, e.Code, name)
}

// Is says a refusal is ErrRefused.
func (*RefusedError) Is(target error) bool { return target == ErrRefused }

// codes names the codes an appliance refuses with, as the reference
// implementation lists them, but for 200 and 202, which refuse nothing,
// and for the one it lists as 412 under the name that is 414's.
//
//nolint:gochecknoglobals // a table, read only
var codes = map[int]string{
	400: "BadRequest", 403: "Forbidden", 404: "NotFound", 405: "MethodNotAllowed",
	413: "RequestEntityTooLong", 414: "RequestUriTooLong", 429: "TooManyRequests",
	500: "InternalServerError", 501: "NotImplemented", 502: "BadGateway", 503: "ServiceUnavailable",
	504: "GatewayTimeout", 507: "InsufficientMemory", 512: "UnknownUID", 513: "WriteRequest UnknownUID",
	514: "ReadRequest UnknownUID", 515: "Busy", 516: "WriteRequest Busy", 517: "ReadRequest Busy",
	518: "NoAccess", 519: "WriteRequest NoAccess", 520: "ReadRequest NoAccess", 521: "NoAccessByList",
	522: "WriteRequest NoAccessByList", 523: "ReadRequest NoAccessByList", 524: "NotAvailable",
	525: "WriteRequest NotAvailable", 526: "ReadRequest NotAvailable", 527: "NotAvailableByList",
	528: "WriteRequest NotAvailableByList", 529: "ReadRequest NotAvailableByList", 530: "NoExecution",
	531: "ValueOutOfRange", 532: "InvalidUIDValue", 533: "Incomplete", 534: "Inconsistent",
	535: "CmdViolation", 536: "InvalidFormat", 537: "RemoteControlNotActive", 538: "RemoteStartNotActive",
	539: "LockedByLocalControl", 540: "DeviceStateNotCompliant", 541: "ProcessStateNotCompliant",
	542: "BackendNotConnected", 543: "EnergyManagementNotConnected", 544: "NotInLocalWiFi",
}

// listed is data as a message carries it: a JSON list, a single value put
// in one; nil is none.
func listed(data any) (json.RawMessage, error) {
	if data == nil {
		return nil, nil
	}

	raw, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("%w: the data is not JSON: %w", ErrArgument, err)
	}

	if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 && trimmed[0] == '[' {
		return trimmed, nil
	}

	return append(append([]byte{'['}, raw...), ']'), nil
}
