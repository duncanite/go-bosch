package homeconnect_test

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"testing"

	"github.com/duncanite/go-bosch/homeconnect"
)

// What the appliances of these tests announce: made up, in the shape the
// open implementations read.
const (
	applianceID = "THERMADOR-DWHD000XXX-000000000001"
	brand       = "THERMADOR"
	kind        = "Dishwasher"
	model       = "DWHD000XXX"
)

// errEnded is how the test's browser ends.
var errEnded = errors.New("the browser's own")

func announcement() homeconnect.Announcement {
	return homeconnect.Announcement{
		Host: "THERMADOR-Dishwasher-000000000001.local.",
		Text: map[string]string{"id": applianceID, "brand": brand, "type": kind, "vib": model, "more": "kept"},
		Addresses: []netip.AddrPort{
			netip.MustParseAddrPort("192.0.2.10:443"),
			{},
			netip.MustParseAddrPort("[2001:db8::10]:443"),
		},
	}
}

// TestRecognize: an appliance is known by the id in its TXT record, and
// says its brand, its kind and its model there; its host's name comes
// without the last dot, its addresses as they came but for one that is
// none, and all its TXT record is kept. The keys are taken whatever
// their case. What carries no id is not an appliance.
func TestRecognize(t *testing.T) {
	t.Parallel()

	want := homeconnect.Found{
		ID: applianceID, Brand: brand, Type: kind, Model: model,
		Host: "THERMADOR-Dishwasher-000000000001.local",
		Addresses: []netip.AddrPort{
			netip.MustParseAddrPort("192.0.2.10:443"),
			netip.MustParseAddrPort("[2001:db8::10]:443"),
		},
		Text: map[string]string{"id": applianceID, "brand": brand, "type": kind, "vib": model, "more": "kept"},
	}

	heard := announcement()

	found, ok := homeconnect.Recognize(heard)
	if !ok || !reflect.DeepEqual(found, want) {
		t.Errorf("Recognize = %+v, %t\nwant %+v", found, ok, want)
	}

	// What is found is the caller's: changing it changes nothing heard.
	found.Text["id"] = "another"
	if heard.Text["id"] != applianceID {
		t.Error("Recognize handed out the announcement's own TXT record")
	}

	folded := homeconnect.Announcement{
		Text: map[string]string{"ID": applianceID, "Brand": brand, "TYPE": kind, "VIB": model},
	}
	if found, ok = homeconnect.Recognize(folded); !ok || found.ID != applianceID || found.Brand != brand ||
		found.Type != kind || found.Model != model || len(found.Addresses) != 0 {
		t.Errorf("Recognize of keys in another case = %+v, %t", found, ok)
	}

	bare := homeconnect.Announcement{Text: map[string]string{"id": applianceID}}
	if found, ok = homeconnect.Recognize(bare); !ok || found.ID != applianceID || found.Brand != "" {
		t.Errorf("Recognize of an id alone = %+v, %t", found, ok)
	}

	for name, other := range map[string]homeconnect.Announcement{
		"no TXT record": {Host: "printer.local."},
		"no id":         {Text: map[string]string{"brand": brand, "type": kind, "vib": model}},
		"an empty id":   {Text: map[string]string{"id": ""}},
	} {
		if found, ok = homeconnect.Recognize(other); ok {
			t.Errorf("Recognize of %s = %+v, taken for an appliance", name, found)
		}
	}
}

// browser tells what it was given, then ends as it was told.
type browser struct {
	heard   []homeconnect.Announcement
	lost    []bool
	service string
	err     error
}

func (b *browser) Browse(_ context.Context, service string, handle func(homeconnect.Announcement, bool)) error {
	b.service = service

	for index, one := range b.heard {
		handle(one, b.lost[index])
	}

	return b.err
}

// TestFind: Find asks the browser for the appliances' service, tells of
// each appliance and of each one gone, of nothing else, and ends with the
// browser's own error; with no browser or no handler it does nothing.
func TestFind(t *testing.T) {
	t.Parallel()

	lent := &browser{
		heard: []homeconnect.Announcement{announcement(), {Host: "printer.local."}, announcement()},
		lost:  []bool{false, false, true},
		err:   errEnded,
	}

	type told struct {
		id   string
		lost bool
	}

	var all []told

	err := homeconnect.Find(t.Context(), lent, func(found homeconnect.Found, lost bool) {
		all = append(all, told{found.ID, lost})
	})
	if !errors.Is(err, errEnded) {
		t.Errorf("Find ends with %v, want the browser's error", err)
	}

	if lent.service != "_homeconnect._tcp" {
		t.Errorf("Find browsed for %q", lent.service)
	}

	if want := []told{{applianceID, false}, {applianceID, true}}; !reflect.DeepEqual(all, want) {
		t.Errorf("Find told of %+v, want %+v", all, want)
	}

	if err = homeconnect.Find(
		t.Context(),
		nil,
		func(homeconnect.Found, bool) {},
	); !errors.Is(
		err,
		homeconnect.ErrArgument,
	) {
		t.Errorf("Find with no browser = %v, want ErrArgument", err)
	}

	if err = homeconnect.Find(t.Context(), lent, nil); !errors.Is(err, homeconnect.ErrArgument) {
		t.Errorf("Find with no handler = %v, want ErrArgument", err)
	}
}
