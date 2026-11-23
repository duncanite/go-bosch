package profile_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/duncanite/go-bosch/homeconnect/profile"
)

// A dishwasher's two documents, cut down: features nested in lists, an
// option a program refers to, an enumeration that is part of another.
const (
	description = `<?xml version="1.0" encoding="UTF-8"?>
<device>
  <description><type>HomeAppliance</type><brand>Fake</brand><model>Fake</model></description>
  <statusList access="read" available="true" uid="0100">
    <status access="read" available="true" enumerationType="0201" refCID="03" refDID="80" uid="020F"/>
    <statusList access="read" available="true" uid="0101">
      <status access="READ" available="true" enumerationType="0228" refCID="03" refDID="80" uid="0228"/>
      <status access="read" available="true" refCID="10" refDID="82" uid="0220"/>
      <status access="read" available="true" refCID="10" refDID="82" uid="7FFFFFFF"/>
      <status access="read" available="true" refCID="10" refDID="82" uid="FFFFFFFF"/>
    </statusList>
  </statusList>
  <settingList access="readWrite" available="true" uid="0102">
    <setting access="readWrite" available="true" enumerationType="0301" refCID="03" refDID="80" uid="0219"/>
    <setting access="readWrite" available="true" refCID="01" refDID="00" uid="1100"/>
  </settingList>
  <eventList uid="0103">
    <event enumerationType="0400" handling="acknowledge" level="hint" refCID="03" refDID="80" uid="0221"/>
  </eventList>
  <commandList access="writeOnly" available="true" uid="0104">
    <command access="writeOnly" available="true" refCID="01" refDID="00" uid="0001"/>
  </commandList>
  <optionList access="readWrite" available="true" uid="0105">
    <option access="readWrite" available="true" refCID="01" refDID="00" uid="1400"/>
  </optionList>
  <programGroup available="true" uid="0106">
    <program available="true" execution="selectAndStart" uid="2001">
      <option access="readWrite" available="true" refUID="1400"/>
    </program>
  </programGroup>
  <activeProgram access="readWrite" validate="true" uid="0100A"/>
  <selectedProgram access="readWrite" fullOptionSet="false" uid="0101A"/>
  <enumerationTypeList>
    <enumerationType enid="0201"><enumeration value="0"/><enumeration value="1"/><enumeration value="2"/></enumerationType>
    <enumerationType enid="0301" subsetOf="0300"><enumeration value="1"/><enumeration value="2"/><enumeration value="9"/></enumerationType>
    <enumerationType enid="0228" subsetOf="0300"><enumeration value="1"/></enumerationType>
  </enumerationTypeList>
</device>`

	mapping = `<?xml version="1.0" encoding="UTF-8"?>
<featureMappingFile>
  <featureDescription>
    <feature refUID="020F">BSH.Common.Status.DoorState</feature>
    <feature refUID="0228">BSH.Common.Status.OperationState</feature>
    <feature refUID="0220"> BSH.Common.Option.RemainingProgramTime </feature>
    <feature refUID="0219">BSH.Common.Setting.PowerState</feature>
    <feature refUID="0221">BSH.Common.Event.ProgramFinished</feature>
    <feature refUID="0001">BSH.Common.Command.AbortProgram</feature>
    <feature refUID="1400">Dishcare.Dishwasher.Option.ExtraDry</feature>
    <feature refUID="2001">Dishcare.Dishwasher.Program.Eco50</feature>
    <feature refUID="100A">BSH.Common.Root.ActiveProgram</feature>
    <feature refUID="101A">BSH.Common.Root.SelectedProgram</feature>
    <feature refUID="7777">Named.And.Not.Described</feature>
  </featureDescription>
  <errorDescription><error refEID="2001">Error.1</error></errorDescription>
  <enumDescriptionList>
    <enumDescription refENID="0201" enumKey="DoorState">
      <enumMember refValue="0">Open</enumMember>
      <enumMember refValue="1">Closed</enumMember>
      <enumMember refValue="2">Locked</enumMember>
    </enumDescription>
    <enumDescription refENID="0228" enumKey="OperationState">
      <enumMember refValue="0">Inactive</enumMember>
      <enumMember refValue="1">Ready</enumMember>
      <enumMember refValue="3">Run</enumMember>
      <enumMember refValue="10">Ten</enumMember>
    </enumDescription>
    <enumDescription refENID="0300" enumKey="PowerState">
      <enumMember refValue="1">Off</enumMember>
      <enumMember refValue="2">On</enumMember>
      <enumMember refValue="3">Standby</enumMember>
    </enumDescription>
  </enumDescriptionList>
</featureMappingFile>`
)

// TestParse: every feature of the description is had under its id, with
// the name the mapping gives it, its kind and its access in lower case,
// wherever it is nested; a list is no feature, nor is an option a program
// refers to. An enumerated feature has its members' names, by their
// numbers in decimal; one whose enumeration is part of another has those
// members of the whole; one the mapping names keeps the mapping's
// members, whatever the description says it is a part of; one whose
// enumeration is named nowhere has none. An id is a feature's up to the
// largest an int holds on every platform, and none beyond.
func TestParse(t *testing.T) {
	t.Parallel()

	parsed, err := profile.Parse([]byte(description), []byte(mapping))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	want := []profile.Feature{
		{UID: 0x0001, Name: "BSH.Common.Command.AbortProgram", Kind: profile.Command, Access: "writeonly"},
		{
			UID: 0x020F, Name: "BSH.Common.Status.DoorState", Kind: profile.Status, Access: "read",
			Values: map[int]string{0: "Open", 1: "Closed", 2: "Locked"},
		},
		{
			UID: 0x0219, Name: "BSH.Common.Setting.PowerState", Kind: profile.Setting, Access: "readwrite",
			Values: map[int]string{1: "Off", 2: "On"},
		},
		{UID: 0x0220, Name: "BSH.Common.Option.RemainingProgramTime", Kind: profile.Status, Access: "read"},
		{UID: 0x0221, Name: "BSH.Common.Event.ProgramFinished", Kind: profile.Event},
		{
			UID: 0x0228, Name: "BSH.Common.Status.OperationState", Kind: profile.Status, Access: "read",
			Values: map[int]string{0: "Inactive", 1: "Ready", 3: "Run", 10: "Ten"},
		},
		{UID: 0x100A, Name: "BSH.Common.Root.ActiveProgram", Kind: profile.ActiveProgram, Access: "readwrite"},
		{UID: 0x101A, Name: "BSH.Common.Root.SelectedProgram", Kind: profile.SelectedProgram, Access: "readwrite"},
		{UID: 0x1100, Kind: profile.Setting, Access: "readwrite"},
		{UID: 0x1400, Name: "Dishcare.Dishwasher.Option.ExtraDry", Kind: profile.Option, Access: "readwrite"},
		{UID: 0x2001, Name: "Dishcare.Dishwasher.Program.Eco50", Kind: profile.Program},
		{UID: 0x7FFFFFFF, Kind: profile.Status, Access: "read"},
	}

	if got := parsed.Features(); !reflect.DeepEqual(got, want) {
		t.Errorf("Features =\n%+v\nwant\n%+v", got, want)
	}

	door, known := parsed.Feature(527)
	if !known || door.Name != "BSH.Common.Status.DoorState" {
		t.Errorf("Feature(527) = %+v, %t", door, known)
	}

	if _, known = parsed.Feature(0x0100); known {
		t.Error("a list of the description is had as a feature")
	}

	if uid, named := parsed.UID("BSH.Common.Status.OperationState"); !named || uid != 552 {
		t.Errorf("UID of the operation state = %d, %t", uid, named)
	}

	for _, name := range []string{"Named.And.Not.Described", "", "BSH.Common.Status.Nothing"} {
		if uid, named := parsed.UID(name); named {
			t.Errorf("UID(%q) = %d, of a feature the description has not", name, uid)
		}
	}

	// What is had is the caller's: changing it changes nothing kept.
	door.Values[0] = "changed"

	if again, _ := parsed.Feature(527); again.Values[0] != "Open" {
		t.Error("Feature handed out the profile's own enumeration")
	}

	door.Values[0] = "Open"

	for value, name := range map[string]string{"0": "Open", "2": "Locked"} {
		if said, is := door.Says(json.RawMessage(value)); !is || said != name {
			t.Errorf("the door at %s says %q, %t; want %q", value, said, is, name)
		}
	}

	if said, is := door.Says(json.RawMessage(" 1\n")); !is || said != "Closed" {
		t.Errorf("the door at a 1 with space about it says %q, %t", said, is)
	}

	for _, value := range []string{"7", "true", `"1"`, "1.5", ""} {
		if said, is := door.Says(json.RawMessage(value)); is {
			t.Errorf("the door at %q says %q, a member it has not", value, said)
		}
	}

	plain, _ := parsed.Feature(0x1100)
	if said, is := plain.Says(json.RawMessage("1")); is {
		t.Errorf("a feature that is not enumerated says %q", said)
	}
}

// archive is a zip of files, each a name and its content.
func archive(t *testing.T, files ...[2]string) []byte {
	t.Helper()

	var packed bytes.Buffer

	writer := zip.NewWriter(&packed)

	for _, file := range files {
		entry, err := writer.Create(file[0])
		if err != nil {
			t.Fatal(err)
		}

		if _, err = entry.Write([]byte(file[1])); err != nil {
			t.Fatal(err)
		}
	}

	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	return packed.Bytes()
}

// TestFromArchive: a profile is read from the archive an account gives,
// its two documents found by how their names end, whatever else it
// holds. An archive that is none, that lacks either document, or whose
// document is no XML, is no profile.
func TestFromArchive(t *testing.T) {
	t.Parallel()

	whole := archive(t,
		[2]string{"README.txt", "not a document"},
		[2]string{"THERMADOR-DWHD000XXX-000000000001_DeviceDescription.xml", description},
		[2]string{"THERMADOR-DWHD000XXX-000000000001_FeatureMapping.xml", mapping},
	)

	parsed, err := profile.FromArchive(whole)
	if err != nil || len(parsed.Features()) != 12 {
		t.Fatalf("FromArchive = %v, with %d features", err, len(parsed.Features()))
	}

	// Of two descriptions in one archive, the first is the appliance's.
	twice := archive(
		t,
		[2]string{"a_DeviceDescription.xml", description},
		[2]string{
			"b_DeviceDescription.xml",
			`<device><statusList uid="0001"><status uid="0002"/></statusList></device>`,
		},
		[2]string{"a_FeatureMapping.xml", mapping},
	)

	first, err := profile.FromArchive(twice)
	if err != nil {
		t.Fatalf("FromArchive of an archive with two descriptions: %v", err)
	}

	if features := first.Features(); len(features) != 12 {
		t.Errorf("an archive with two descriptions gives %d features, want the first's 12", len(features))
	}

	for name, packed := range map[string][]byte{
		"what is no zip":          []byte("not a zip"),
		"nothing":                 nil,
		"no description":          archive(t, [2]string{"a_FeatureMapping.xml", mapping}),
		"no mapping":              archive(t, [2]string{"a_DeviceDescription.xml", description}),
		"a description not XML":   archive(t, [2]string{"a_DeviceDescription.xml", "<device><status"}, [2]string{"a_FeatureMapping.xml", mapping}),
		"a mapping not XML":       archive(t, [2]string{"a_DeviceDescription.xml", description}, [2]string{"a_FeatureMapping.xml", "<x></y>"}),
		"an empty description":    archive(t, [2]string{"a_DeviceDescription.xml", ""}, [2]string{"a_FeatureMapping.xml", mapping}),
		"a mapping of no element": archive(t, [2]string{"a_DeviceDescription.xml", description}, [2]string{"a_FeatureMapping.xml", "  "}),
	} {
		if parsed, err = profile.FromArchive(packed); !errors.Is(err, profile.ErrProfile) || parsed != nil {
			t.Errorf("FromArchive of %s = %v, want ErrProfile", name, err)
		}
	}

	// A document as long as the bound allows is read; one longer is not.
	padded := description + strings.Repeat(" ", 16<<20-len(description)+1)
	if _, err = profile.Parse([]byte(padded), []byte(mapping)); !errors.Is(err, profile.ErrProfile) {
		t.Errorf("Parse of a description past the bound = %v, want ErrProfile", err)
	}

	bomb := archive(t, [2]string{"a_DeviceDescription.xml", padded}, [2]string{"a_FeatureMapping.xml", mapping})
	if len(bomb) > 1<<20 {
		t.Fatalf("the test's archive did not shrink: %d bytes", len(bomb))
	}

	if _, err = profile.FromArchive(bomb); !errors.Is(err, profile.ErrProfile) {
		t.Errorf("FromArchive of a document that unpacks past the bound = %v, want ErrProfile", err)
	}
}
