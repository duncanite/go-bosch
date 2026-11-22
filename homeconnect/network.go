package homeconnect

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"strings"
)

// Service is the service a Home Connect appliance announces itself as on
// the local network, over DNS-SD, and what a search for appliances asks
// for.
const Service = "_homeconnect._tcp"

// ErrArgument is a call that was not given what it needs; nothing was
// done.
var ErrArgument = errors.New("homeconnect: bad argument")

// Announcement is one instance of a service as it was heard on the
// network: what this package reads of it, and nothing of the means that
// heard it.
type Announcement struct {
	// Host is the name of the host it is on, as announced.
	Host string
	// Text is the entries of its TXT record, by their keys, in whatever
	// case those came.
	Text map[string]string
	// Addresses is where it was heard to be, each an address and the port
	// its service is announced on.
	Addresses []netip.AddrPort
}

// Browser is the means of finding services on the local network, which this
// package does not have and is lent: mDNS's port is one to a host, and it
// is the program's. Whatever browses satisfies it with a few lines.
type Browser interface {
	// Browse tells handle of the instances of a service: each one it
	// knows of or hears, and each change to one, whole as it then stands,
	// with lost false; and each one that is gone, as it last stood, with
	// lost true. Calls come one at a time and in order, for as long as
	// ctx lasts. It returns ctx's error when ctx ends, and its own when
	// it can browse no more.
	//
	// One that is gone has to come with its TXT record: Find knows an
	// appliance by it.
	Browse(ctx context.Context, service string, handle func(announcement Announcement, lost bool)) error
}

// Found is what a device on the network says of itself: that it is a Home
// Connect appliance, which one, and where.
//
// An announcement is anyone's to make, and proves nothing. What opens an
// appliance is the key of its profile, which is not on the network: it
// comes from the owner's Home Connect account, and a device that is not
// the appliance does not have it.
type Found struct {
	// ID is who it says it is: the appliance's Home Connect id, which its
	// profile names it by.
	ID string
	// Brand, Type and Model are what it says it is: its maker's brand, the
	// kind of appliance, and its model as BSH abbreviates it. Any may be
	// empty.
	Brand string
	Type  string
	Model string
	// Host is the name of the host it says it is on.
	Host string
	// Addresses is where it says it is, in the order they came, each with
	// the port it announces: none when the announcement had none. Which
	// of several reaches the appliance from here is the caller's to know,
	// or to try.
	Addresses []netip.AddrPort
	// Text is everything its TXT record held, by key, the entries above
	// among them.
	Text map[string]string
}

// The keys of an appliance's TXT record this package reads.
const (
	keyID    = "id"
	keyBrand = "brand"
	keyType  = "type"
	keyModel = "vib"
)

// Recognize says whether an instance of Service is a Home Connect
// appliance's, and which appliance: its TXT record carries its id. An
// instance with none is not taken for one.
func Recognize(announcement Announcement) (Found, bool) {
	named := text(announcement, keyID)
	if named == "" {
		return Found{}, false
	}

	found := Found{
		ID:        named,
		Brand:     text(announcement, keyBrand),
		Type:      text(announcement, keyType),
		Model:     text(announcement, keyModel),
		Host:      strings.TrimSuffix(announcement.Host, "."),
		Addresses: make([]netip.AddrPort, 0, len(announcement.Addresses)),
		Text:      make(map[string]string, len(announcement.Text)),
	}

	for _, address := range announcement.Addresses {
		if address.IsValid() {
			found.Addresses = append(found.Addresses, address)
		}
	}

	maps.Copy(found.Text, announcement.Text)

	return found, true
}

// text is an entry of an announcement's TXT record. The keys of a TXT
// record are matched whatever their case (RFC 6763, section 6.4), and a
// browser may hand them as announced or folded; the key as written wins,
// and of two that differ from it by case alone, either.
func text(announcement Announcement, key string) string {
	if value, ok := announcement.Text[key]; ok {
		return value
	}

	for name, value := range announcement.Text {
		if strings.EqualFold(name, key) {
			return value
		}
	}

	return ""
}

// Find tells handle of the Home Connect appliances on the local network,
// through the browser it is lent: each one found, and again each time the
// browser tells of it again, whether or not what Found holds changed, with
// lost false; each one gone, with lost true. It is the browser's Browse
// for Service, narrowed to what Recognize takes, with nothing kept between
// two calls, and lasts and ends as that does.
func Find(ctx context.Context, browser Browser, handle func(found Found, lost bool)) error {
	if browser == nil || handle == nil {
		return fmt.Errorf("%w: Find needs a browser and a handler", ErrArgument)
	}

	return browser.Browse(ctx, Service, func(announcement Announcement, lost bool) {
		if found, ok := Recognize(announcement); ok {
			handle(found, lost)
		}
	})
}
