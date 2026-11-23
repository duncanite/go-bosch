// Package profile reads what a Home Connect appliance's profile says of
// its values: the name each numeric id stands for, what kind of thing it
// is, and the names of what an enumerated one may be. An appliance speaks
// in numbers; its profile, which the owner's account gives beside its
// key, is what names them.
//
// A profile is two XML documents, a description of the appliance's
// structure and a mapping of its ids to names, which the account gives
// in one archive. They are a stranger's text: read within bounds, and
// what they name is shown, never obeyed.
//
// The standard library alone.
package profile

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// largest bounds each of a profile's two documents, and the archive that
// holds them. A dishwasher's are some hundreds of kilobytes.
const largest = 16 << 20

var (
	// ErrProfile is a profile that cannot be read: an archive that is
	// none or lacks one of the two documents, or a document that is no
	// XML of the kind.
	ErrProfile = errors.New("profile: cannot be read")
	// ErrArgument is a call that was not given what it needs.
	ErrArgument = errors.New("profile: bad argument")
)

// Kind is what a feature is.
type Kind string

// The kinds of feature a profile describes.
const (
	// Status is a state the appliance reports and no one sets.
	Status Kind = "status"
	// Setting is a state that is set.
	Setting Kind = "setting"
	// Event is something the appliance signals.
	Event Kind = "event"
	// Command is an action the appliance takes when told.
	Command Kind = "command"
	// Option is an option of a program.
	Option Kind = "option"
	// Program is a program the appliance runs.
	Program Kind = "program"
	// ActiveProgram holds the id of the program that runs.
	ActiveProgram Kind = "activeProgram"
	// SelectedProgram holds the id of the program that is selected.
	SelectedProgram Kind = "selectedProgram"
)

// kinds is the elements of a description that are features, by name.
//
//nolint:gochecknoglobals // a table, read only
var kinds = map[string]Kind{
	"status": Status, "setting": Setting, "event": Event, "command": Command, "option": Option,
	"program": Program, "activeProgram": ActiveProgram, "selectedProgram": SelectedProgram,
}

// Feature is one thing an appliance has, under its numeric id.
type Feature struct {
	// UID is the id the appliance writes its value under.
	UID int
	// Name is what the profile calls it, "BSH.Common.Status.DoorState";
	// empty when the profile names the id nowhere.
	Name string
	// Kind is what it is.
	Kind Kind
	// Access is how it may be used, in lower case as the appliance's own
	// words are compared: read, readwrite, writeonly, readstatic, none.
	// Empty when the description does not say.
	Access string
	// Values is the names of what it may be when it is enumerated, by the
	// number the appliance writes; nil when it is not.
	Values map[int]string
}

// Says is a value of the feature as the profile names it: the member's
// name when the feature is enumerated and the value is one of its
// numbers. It says whether there was one; a value that is no member, as
// some firmware sends, has none, and is shown as it came.
func (f Feature) Says(value json.RawMessage) (string, bool) {
	if f.Values == nil {
		return "", false
	}

	number, err := strconv.Atoi(strings.TrimSpace(string(value)))
	if err != nil {
		return "", false
	}

	name, known := f.Values[number]

	return name, known
}

// Profile is the features of one appliance.
type Profile struct {
	features map[int]Feature
	named    map[string]int
}

// Feature is the feature an id stands for, and whether the profile has
// it.
func (p *Profile) Feature(uid int) (Feature, bool) {
	feature, known := p.features[uid]
	feature.Values = maps.Clone(feature.Values)

	return feature, known
}

// UID is the id of the feature a name stands for, and whether the profile
// has it.
func (p *Profile) UID(name string) (int, bool) {
	uid, known := p.named[name]

	return uid, known
}

// Features is every feature of the profile, in the order of their ids.
func (p *Profile) Features() []Feature {
	features := make([]Feature, 0, len(p.features))
	for _, uid := range slices.Sorted(maps.Keys(p.features)) {
		feature, _ := p.Feature(uid)
		features = append(features, feature)
	}

	return features
}

// The two documents of a profile, by how their names end in the archive.
const (
	descriptionSuffix = "_DeviceDescription.xml"
	mappingSuffix     = "_FeatureMapping.xml"
)

// FromArchive reads a profile from the archive the account gives for an
// appliance: a zip that holds the appliance's description and its feature
// mapping, each under a name that ends as those documents' names do.
func FromArchive(archive []byte) (*Profile, error) {
	if len(archive) > largest {
		return nil, fmt.Errorf("%w: an archive of %d bytes", ErrProfile, len(archive))
	}

	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("%w: not a zip archive: %w", ErrProfile, err)
	}

	var description, mapping []byte

	for _, file := range reader.File {
		switch {
		case strings.HasSuffix(file.Name, descriptionSuffix) && description == nil:
			description, err = unpacked(file)
		case strings.HasSuffix(file.Name, mappingSuffix) && mapping == nil:
			mapping, err = unpacked(file)
		}

		if err != nil {
			return nil, err
		}
	}

	if description == nil || mapping == nil {
		return nil, fmt.Errorf("%w: the archive lacks a description or a feature mapping", ErrProfile)
	}

	return Parse(description, mapping)
}

// unpacked is one file of an archive, read within the bound whatever
// length the archive claims for it.
func unpacked(file *zip.File) ([]byte, error) {
	opened, err := file.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrProfile, err)
	}

	defer func() { _ = opened.Close() }()

	content, err := io.ReadAll(io.LimitReader(opened, largest+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrProfile, err)
	}

	if len(content) > largest {
		return nil, fmt.Errorf("%w: a document longer than %d bytes", ErrProfile, largest)
	}

	return content, nil
}

// Parse reads a profile from its two documents: the appliance's
// description and its feature mapping.
func Parse(description, mapping []byte) (*Profile, error) {
	if len(description) > largest || len(mapping) > largest {
		return nil, fmt.Errorf("%w: a document longer than %d bytes", ErrProfile, largest)
	}

	names, members, err := mapped(mapping)
	if err != nil {
		return nil, err
	}

	features, subsets, err := described(description)
	if err != nil {
		return nil, err
	}

	resolve(members, subsets)

	profile := &Profile{features: make(map[int]Feature, len(features)), named: make(map[string]int, len(features))}

	for _, one := range features {
		feature := Feature{UID: one.uid, Name: names[one.uid], Kind: one.kind, Access: one.access}

		if one.enumerated {
			if values, known := members[one.enid]; known {
				feature.Values = maps.Clone(values)
			}
		}

		profile.features[feature.UID] = feature

		if feature.Name != "" {
			profile.named[feature.Name] = feature.UID
		}
	}

	return profile, nil
}

// resolve gives each enumeration the mapping does not name the members it
// is said to be a part of: the ones listed, under the names of the whole.
func resolve(members map[int]map[int]string, subsets map[int]subset) {
	for enid, part := range subsets {
		if _, named := members[enid]; named {
			continue
		}

		whole, known := members[part.of]
		if !known {
			continue
		}

		listed := make(map[int]string, len(part.values))

		for _, value := range part.values {
			if name, has := whole[value]; has {
				listed[value] = name
			}
		}

		members[enid] = listed
	}
}

// hexadecimal is a number of a profile, written in hexadecimal: one an
// int holds wherever this is built, and never below zero.
func hexadecimal(text string) (int, bool) {
	const base, bits = 16, 31

	number, err := strconv.ParseUint(strings.TrimSpace(text), base, bits)

	return int(number), err == nil
}

// attribute is an attribute of an element, by its name; empty when the
// element has none.
func attribute(element xml.StartElement, name string) string {
	for _, one := range element.Attr {
		if one.Name.Local == name {
			return one.Value
		}
	}

	return ""
}

// named is a number of a feature mapping with the name it is given.
type named struct {
	UID   string `xml:"refUID,attr"`
	Value string `xml:"refValue,attr"`
	Name  string `xml:",chardata"`
}

// enumerated is an enumeration of a feature mapping: its members' names.
type enumerated struct {
	ENID    string  `xml:"refENID,attr"`
	Members []named `xml:"enumMember"`
}

// mapping is a feature mapping as it is written.
type mapping struct {
	Features     []named      `xml:"featureDescription>feature"`
	Enumerations []enumerated `xml:"enumDescriptionList>enumDescription"`
}

// mapped reads a feature mapping: the name of each feature, and the names
// of each enumeration's members. What carries no number is passed over.
func mapped(document []byte) (names map[int]string, members map[int]map[int]string, err error) {
	var read mapping

	if err = xml.Unmarshal(document, &read); err != nil {
		return nil, nil, fmt.Errorf("%w: the feature mapping: %w", ErrProfile, err)
	}

	names, members = map[int]string{}, map[int]map[int]string{}

	for _, one := range read.Features {
		if uid, numbered := hexadecimal(one.UID); numbered {
			names[uid] = strings.TrimSpace(one.Name)
		}
	}

	for _, one := range read.Enumerations {
		enid, numbered := hexadecimal(one.ENID)
		if !numbered {
			continue
		}

		members[enid] = map[int]string{}

		for _, member := range one.Members {
			if value, valueErr := strconv.Atoi(strings.TrimSpace(member.Value)); valueErr == nil {
				members[enid][value] = strings.TrimSpace(member.Name)
			}
		}
	}

	return names, members, nil
}

// feature is a feature as a description writes it.
type feature struct {
	uid        int
	kind       Kind
	access     string
	enid       int
	enumerated bool
}

// subset is an enumeration that is part of another.
type subset struct {
	of     int
	values []int
}

// reading is a description being read: the features met so far, the
// enumerations said to be parts of others, and the one being listed.
type reading struct {
	features []feature
	subsets  map[int]subset
	within   int
}

// start takes one element of a description.
func (r *reading) start(element xml.StartElement) {
	switch kind, isFeature := kinds[element.Name.Local]; {
	case isFeature:
		// An option inside a program refers to one described elsewhere,
		// and carries no id of its own.
		uid, numbered := hexadecimal(attribute(element, "uid"))
		if !numbered {
			return
		}

		one := feature{uid: uid, kind: kind, access: strings.ToLower(attribute(element, "access"))}
		one.enid, one.enumerated = hexadecimal(attribute(element, "enumerationType"))
		r.features = append(r.features, one)
	case element.Name.Local == "enumerationType":
		r.within = -1

		enid, numbered := hexadecimal(attribute(element, "enid"))
		of, part := hexadecimal(attribute(element, "subsetOf"))

		if numbered && part {
			r.within = enid
			r.subsets[enid] = subset{of: of}
		}
	case element.Name.Local == "enumeration" && r.within >= 0:
		if value, err := strconv.Atoi(strings.TrimSpace(attribute(element, "value"))); err == nil {
			held := r.subsets[r.within]
			held.values = append(held.values, value)
			r.subsets[r.within] = held
		}
	}
}

// described reads a description: its features, wherever they are nested,
// and the enumerations it says are parts of others.
func described(description []byte) ([]feature, map[int]subset, error) {
	decoder := xml.NewDecoder(bytes.NewReader(description))
	read := reading{subsets: map[int]subset{}, within: -1}
	root := false

	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return nil, nil, fmt.Errorf("%w: the description: %w", ErrProfile, err)
		}

		if element, starts := token.(xml.StartElement); starts {
			root = true

			read.start(element)
		}
	}

	if !root {
		return nil, nil, fmt.Errorf("%w: the description holds no element", ErrProfile)
	}

	return read.features, read.subsets, nil
}
