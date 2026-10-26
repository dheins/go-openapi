// Package service is an annotated pet service. Its document,
// ../openapi.yaml, is what goapi scan makes of it (codescan's tests check
// that it is up to date), and the scanned golden case generates a server
// and client from that document.
//
//goapi:info title="Scanned pets" version=1.0.0
//goapi:server https://pets.example.com/v1
//goapi:securityScheme apiKey type=apiKey in=header name=X-API-Key
//goapi:tag pets description="Pets and their photos"
package service

import (
	"context"

	"github.com/dheins/go-openapi/oapi"
)

// Pet is a pet in the store.
type Pet struct {
	ID   int64                 `json:"id" goapi:"readOnly"`
	Name string                `json:"name" goapi:"minLength=1;maxLength=40"`
	Kind Kind                  `json:"kind"`
	Tag  oapi.Optional[string] `json:"tag,omitzero"`
	Born oapi.Date             `json:"born,omitzero"`
	Diet Diet                  `json:"diet,omitempty"`
}

// Diet is what a pet eats.
//
//goapi:oneOf discriminator=type
type Diet interface{ diet() }

// Herbivore eats plants.
type Herbivore struct {
	Type   string   `json:"type"`
	Plants []string `json:"plants,omitempty"`
}

func (Herbivore) diet() {}

// Carnivore eats meat.
//
//goapi:discriminatorValue meat
type Carnivore struct {
	Type string `json:"type"`
	Prey string `json:"prey" goapi:"minLength=1"`
}

func (*Carnivore) diet() {}

// Kind is the kind of a pet.
//
//goapi:enum
type Kind string

const (
	Dog Kind = "dog"
	Cat Kind = "cat"
)

// Problem is an error.
type Problem struct {
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
}

// Store serves the pets; its methods are annotated, not implemented.
type Store struct{}

// ListPetsRequest are the filters of ListPets.
type ListPetsRequest struct {
	// Limit is the most pets to return.
	Limit int32 `goapi:"in=query;name=limit;minimum=1;maximum=100;default=20"`
	// Kinds are the kinds to return.
	Kinds []Kind `goapi:"in=query;name=kind;explode=false"`
}

// PetPage is a page of pets, with the total in a header.
type PetPage struct {
	Total int64 `goapi:"in=header;name=X-Total;required"`
	Pets  []Pet `goapi:"in=body"`
}

// ListPets lists the pets.
//
//goapi:operation GET /pets tags=pets
//goapi:response 200 PetPage
//goapi:example query:limit 10
//goapi:example 200 header:X-Total 2
//goapi:response default Problem application/problem+json
func (Store) ListPets(ctx context.Context, req *ListPetsRequest) (*PetPage, error) {
	return nil, nil
}

// CreatePetRequest holds the new pet.
type CreatePetRequest struct {
	Pet Pet `goapi:"in=body"`
}

// CreatePet adds a pet.
//
//goapi:operation POST /pets tags=pets
//goapi:response 201 Pet
//goapi:link 201 GetPet operationId=getPet id=$response.body#/id
//goapi:response 409 Problem application/problem+json
//goapi:example request {"name": "Rex", "kind": "dog"}
func (Store) CreatePet(ctx context.Context, req *CreatePetRequest) (*Pet, error) {
	return nil, nil
}

// PetRequest names a pet.
type PetRequest struct {
	ID int64 `goapi:"in=path;name=id;minimum=1"`
}

// GetPet returns a pet.
//
//goapi:operation GET /pets/{id} tags=pets
//goapi:response 200 Pet
//goapi:example 200 name=rex summary="A dog" {"id": 1, "name": "Rex", "kind": "dog"}
//goapi:response 404
func (Store) GetPet(ctx context.Context, req *PetRequest) (*Pet, error) {
	return nil, nil
}

// DeletePet removes a pet.
//
//goapi:operation DELETE /pets/{id} tags=pets
//goapi:response 204
//goapi:security apiKey
func (Store) DeletePet(ctx context.Context, req *PetRequest) error {
	return nil
}

// PhotoRequest holds a pet's photo.
type PhotoRequest struct {
	ID    int64  `goapi:"in=path;name=id"`
	Photo []byte `goapi:"in=body;mediaType=image/png"`
}

// PutPhoto stores a pet's photo.
//
//goapi:operation PUT /pets/{id}/photo tags=pets
//goapi:response 204
func (Store) PutPhoto(ctx context.Context, req *PhotoRequest) error {
	return nil
}

// Upload is a pet's photo with a caption.
type Upload struct {
	Photo   oapi.File `json:"photo" goapi:"contentType=image/png|image/jpeg;headers=X-Checksum"`
	Caption string    `json:"caption,omitempty" goapi:"maxLength=40"`
}

// UploadRequest holds a pet's upload.
type UploadRequest struct {
	ID     int64  `goapi:"in=path;name=id"`
	Upload Upload `goapi:"in=body;mediaType=multipart/form-data"`
}

// UploadPhoto stores a pet's photo, with its caption.
//
//goapi:operation POST /pets/{id}/uploads tags=pets
//goapi:response 204
func (Store) UploadPhoto(ctx context.Context, req *UploadRequest) error {
	return nil
}

// Adoption is sent when a pet is adopted.
type Adoption struct {
	Pet Pet    `json:"pet"`
	By  string `json:"by" goapi:"minLength=1"`
}

// AdoptedRequest is the request of the petAdopted webhook.
type AdoptedRequest struct {
	Adoption Adoption `goapi:"in=body"`
}

// PetAdopted tells a consumer that a pet was adopted, with the key the
// consumer issued.
//
//goapi:webhook petAdopted POST tags=pets
//goapi:response 204
//goapi:security apiKey
func (Store) PetAdopted(ctx context.Context, req *AdoptedRequest) error {
	return nil
}

// SubscribeRequest asks for pet events at a URL.
type SubscribeRequest struct {
	Body Subscription `goapi:"in=body"`
}

// Subscription is where to send pet events.
type Subscription struct {
	URL string `json:"url" goapi:"format=uri"`
}

// Subscribe registers a URL for pet events.
//
//goapi:operation POST /subscriptions tags=pets
//goapi:response 201
func (Store) Subscribe(ctx context.Context, req *SubscribeRequest) error {
	return nil
}

// PetEventRequest is a pet event.
type PetEventRequest struct {
	Pet Pet `goapi:"in=body"`
}

// NotifyPet sends a subscriber a pet event, with a key or none.
//
//goapi:callback subscribe petEvent POST {$request.body#/url}/pets
//goapi:response 204
//goapi:security apiKey
//goapi:security -
func (Store) NotifyPet(ctx context.Context, req *PetEventRequest) error {
	return nil
}
