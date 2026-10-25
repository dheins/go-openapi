// Package models is a fixture of codescan: Go types and the schemas they
// become.
package models

import (
	"encoding/json"
	"net/netip"
	"time"

	"github.com/dheins/go-openapi/codescan/testdata/models/shared"
	"github.com/dheins/go-openapi/oapi"
)

// Pet is a pet in the store.
//
// It is the main type of the fixture.
//
//goapi:schema
type Pet struct {
	// ID is assigned by the store.
	ID   int64                 `json:"id" goapi:"readOnly"`
	Name string                `json:"name" goapi:"minLength=1;maxLength=50;example=Rex"`
	Kind Kind                  `json:"kind" goapi:"default=dog"`
	Tag  oapi.Optional[string] `json:"tag,omitzero"`
	// Nickname may be null.
	Nickname *string               `json:"nickname"`
	Owner    *Owner                `json:"owner,omitempty"`
	Tags     []string              `json:"tags,omitempty" goapi:"maxItems=10;uniqueItems"`
	Scores   map[string]float64    `json:"scores,omitempty"`
	Born     oapi.Date             `json:"born,omitzero"`
	Seen     time.Time             `json:"seen"`
	Photo    []byte                `json:"photo,omitempty"`
	Weight   float32               `json:"weight,string"`
	Age      uint8                 `json:"age" goapi:"maximum=40"`
	Extra    json.RawMessage       `json:"extra,omitempty"`
	Anything any                   `json:"anything,omitempty"`
	Parent   *Pet                  `json:"parent,omitempty"`
	Friends  [2]PetID              `json:"friends"`
	Address  netip.Addr            `json:"address,omitzero"`
	Note     oapi.Nullable[string] `json:"note" goapi:"pattern=^[a-z\\;]*$"`
	secret   string
	Ignored  string `json:"-"`
	Audit
	Meta `json:"meta"`

	// Deprecated: use Tags.
	Label string `json:"label,omitempty"`
}

// Audit is embedded in Pet; its fields are Pet's.
type Audit struct {
	Created time.Time `json:"created"`
	By      string    `json:"by,omitempty"`
}

// Meta is embedded in Pet under a name.
type Meta struct {
	Version int `json:"version"`
}

// Kind is the kind of a pet.
//
//goapi:enum
type Kind string

const (
	KindDog Kind = "dog"
	KindCat Kind = "cat"
	// Not a Kind.
	other = "other"
)

// Level is a numeric enum.
//
//goapi:enum
type Level int

const (
	Low  Level = 1
	High Level = 3
)

// PetID identifies a pet.
type PetID int64

// Owner is a pet's owner; its name clashes with shared.Owner.
type Owner struct {
	Name  string        `json:"name"`
	Other *shared.Owner `json:"other,omitempty"`
	Level Level         `json:"level"`
}

// Page is a page of items.
type Page[T any] struct {
	Items []T    `json:"items"`
	Next  string `json:"next,omitempty"`
}

// Pets is a page of pets, named by the directive.
//
//goapi:schema PetPage
type Pets Page[Pet]

// Upload holds a file.
//
//goapi:schema
type Upload struct {
	File  oapi.File   `json:"file"`
	Done  chan bool   `json:"done,omitempty"`
	Pages Page[Owner] `json:"pages,omitzero"`
}

// Diet is what a pet eats.
//
//goapi:oneOf discriminator=kind
type Diet interface{ Eats() string }

// Herbivore eats plants.
type Herbivore struct {
	Kind   string   `json:"kind"`
	Plants []string `json:"plants"`
}

// Eats makes Herbivore a Diet.
func (Herbivore) Eats() string { return "plants" }

// Carnivore eats meat; a Diet through its pointer.
//
//goapi:discriminatorValue meat
type Carnivore struct {
	Kind string `json:"kind"`
	Prey string `json:"prey"`
}

// Eats makes *Carnivore a Diet.
func (*Carnivore) Eats() string { return "meat" }

// Contact is how to reach an owner: any of its variants may match.
//
//goapi:anyOf
type Contact interface{ contact() }

// Email is an email address.
type Email string

func (Email) contact() {}

// Phone is a phone number.
type Phone struct {
	Number string `json:"number"`
}

func (Phone) contact() {}

// Feeding uses the unions.
//
//goapi:schema
type Feeding struct {
	Diet    Diet    `json:"diet"`
	Contact Contact `json:"contact,omitempty"`
	Backup  *Diet   `json:"backup"`
}
