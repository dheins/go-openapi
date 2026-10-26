package service

import (
	"context"
	"net/http"

	"github.com/dheins/go-openapi/codescan/testdata/models"
	"github.com/dheins/go-openapi/oapi"
)

// Server implements the pet service.
type Server struct{}

// ListPetsRequest holds the inputs of ListPets.
type ListPetsRequest struct {
	// Limit is how many pets to return.
	Limit int32 `goapi:"in=query;name=limit;minimum=1;maximum=100;default=20"`
	// Tags filters by tag.
	Tags []string `goapi:"in=query;name=tags;explode=false"`
	// Trace is a request id.
	Trace string `goapi:"in=header;name=X-Trace;format=uuid"`
	// Session is the session cookie.
	Session string `goapi:"in=cookie;name=session;required"`
	ignored string
	Unused  string
}

// PetList is a page of pets with the total in a header.
type PetList struct {
	Total int64        `goapi:"in=header;name=X-Total;required"`
	Pets  []models.Pet `goapi:"in=body"`
}

// Problem is an error.
type Problem struct {
	Title  string `json:"title"`
	Status int    `json:"status"`
}

// ListPets lists the pets.
//
// It returns a page at a time.
//
//goapi:operation GET /pets tags=pets summary="List pets"
//goapi:response 200 PetList
//goapi:example 200 []
//goapi:response default Problem application/problem+json description="An error."
//goapi:security -
func (s *Server) ListPets(ctx context.Context, req *ListPetsRequest) (*PetList, error) {
	return nil, nil
}

// GetPetRequest holds the inputs of GetPet.
type GetPetRequest struct {
	ID int64 `goapi:"in=path;name=id;minimum=1"`
}

// GetPet returns a pet.
//
//goapi:operation GET /pets/{id} tags=pets
//goapi:response 200 models.Pet
//goapi:response 404
//goapi:security bearer
//goapi:security oauth:read
func (s *Server) GetPet(ctx context.Context, req GetPetRequest) (models.Pet, error) {
	return models.Pet{}, nil
}

// CreatePetRequest holds the inputs of CreatePet.
type CreatePetRequest struct {
	// Pet is the new pet.
	Pet models.Pet `goapi:"in=body;mediaType=application/json|application/xml"`
}

// CreatePet adds a pet; a plain handler, with its types in directives.
//
// Deprecated: use the bulk endpoint.
//
//goapi:operation POST /pets id=addPet
//goapi:request CreatePetRequest
//goapi:response 201 models.Pet
//goapi:link 201 GetPet operationId=getPet id=$response.body#/id description="The new pet."
//goapi:response 4XX Problem application/problem+json
//goapi:example 4XX {"title": "Conflict", "status": 409}
func CreatePet(w http.ResponseWriter, r *http.Request) {}

// PhotoRequest holds the inputs of photo operations.
type PhotoRequest struct {
	ID    int64                 `goapi:"in=path;name=id"`
	Photo []byte                `goapi:"in=body;mediaType=image/png"`
	Note  oapi.Optional[string] `goapi:"in=query;name=note;maxLength=10"`
}

// PutPhoto stores a pet's photo.
//
//goapi:operation PUT /pets/{id}/photo
//goapi:response 204
func (s *Server) PutPhoto(ctx context.Context, req *PhotoRequest) error { return nil }

// GetPhoto returns a pet's photo.
//
//goapi:operation GET /pets/{id}/photo
//goapi:request GetPetRequest
//goapi:response 200 []byte image/png
func (s *Server) GetPhoto(w http.ResponseWriter, r *http.Request) {}

// Purge removes old pets, with a method of 3.2's additionalOperations.
//
//goapi:operation PURGE /pets
//goapi:response 204
//goapi:security apiKey bearer
func (s *Server) Purge(ctx context.Context) error { return nil }

// AdoptedEvent is sent when a pet is adopted.
type AdoptedEvent struct {
	Pet models.Pet `json:"pet"`
	By  string     `json:"by" goapi:"minLength=1"`
}

// AdoptedRequest is the request of the adopted webhook.
type AdoptedRequest struct {
	Event   AdoptedEvent `goapi:"in=body"`
	Attempt int32        `goapi:"in=header;name=X-Attempt"`
}

// PetAdopted tells a consumer that a pet was adopted.
//
//goapi:webhook petAdopted POST tags=pets
//goapi:response 204
//goapi:response 410 description="The consumer is gone."
//goapi:security bearer
func (s *Server) PetAdopted(ctx context.Context, req *AdoptedRequest) error { return nil }

// SubscribeRequest asks for pet events at a URL.
type SubscribeRequest struct {
	Body struct {
		URL string `json:"url" goapi:"format=uri"`
	} `goapi:"in=body"`
}

// Subscribe registers a URL for pet events.
//
//goapi:operation POST /subscriptions
//goapi:response 201
//goapi:example request application/json name=hooks summary="A \"hook\" URL" {"url": "https://example.com/hooks"}
//goapi:example request name=other {"url": "https://example.com/other"}
func (s *Server) Subscribe(ctx context.Context, req *SubscribeRequest) error { return nil }

// PetEvent is a pet event sent to a subscriber.
type PetEvent struct {
	Pet models.Pet `goapi:"in=body"`
}

// NotifyPet sends a subscriber a pet event, at the URL it gave.
//
//goapi:callback subscribe petEvent POST {$request.body#/url}/pets
//goapi:response 204
//goapi:security apiKey
//goapi:security -
func (s *Server) NotifyPet(ctx context.Context, req *PetEvent) error { return nil }

// PingSubscriber checks that a subscriber is there.
//
//goapi:callback subscribe petEvent GET {$request.body#/url}/pets id=pingSubscriber
//goapi:response 200
func (s *Server) PingSubscriber(ctx context.Context) error { return nil }

// Upload is a pet's photo, with a caption and labels.
type Upload struct {
	Photo   oapi.File `json:"photo" goapi:"contentType=image/png|image/jpeg;headers=X-Checksum|X-Seq"`
	Caption string    `json:"caption,omitempty" goapi:"maxLength=40"`
	Labels  []string  `json:"labels,omitempty" goapi:"style=form;explode=false"`
}

// UploadRequest holds an upload.
type UploadRequest struct {
	ID     int64  `goapi:"in=path;name=id"`
	Upload Upload `goapi:"in=body;mediaType=multipart/form-data"`
}

// UploadPhoto stores a photo with its caption.
//
//goapi:operation POST /pets/{id}/uploads
//goapi:response 204
func (s *Server) UploadPhoto(ctx context.Context, req *UploadRequest) error { return nil }
