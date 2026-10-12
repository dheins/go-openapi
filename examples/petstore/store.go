package main

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"sync"

	"github.com/dheins/go-openapi/examples/petstore/gen/api"
	"github.com/dheins/go-openapi/examples/petstore/gen/model"
	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/problem"
	"github.com/dheins/go-openapi/runtime/server"
)

// store implements api.Handler, keeping pets in memory.
type store struct {
	mu       sync.Mutex
	pets     []model.Pet // ordered by ID
	nextID   int64
	watchers []chan model.Pet
}

func newStore() *store {
	return &store{nextID: 1}
}

func (s *store) GetHealth(_ context.Context, req *api.GetHealthRequest) (api.GetHealthResponse, error) {
	resp := api.GetHealth200TextResponse{}
	resp.Body = "OK"
	return resp, nil
}

func (s *store) ListPets(_ context.Context, req *api.ListPetsRequest) (api.ListPetsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	page := []model.Pet{}
	for _, p := range s.pets {
		if after, ok := req.After.Get(); ok && p.ID <= after {
			continue
		}
		if statuses, ok := req.Status.Get(); ok && !slices.Contains(statuses, p.Status) {
			continue
		}
		page = append(page, p)
	}
	resp := api.ListPets200JSONResponse{}
	if len(page) > int(req.Limit) {
		page = page[:req.Limit]
		resp.XNext = oapi.Some(page[len(page)-1].ID)
	}
	resp.Body = page
	return resp, nil
}

func (s *store) CreatePet(_ context.Context, req *api.CreatePetRequest) (api.CreatePetResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.pets {
		if p.Name == req.Body.Name {
			return nil, problem.Newf(http.StatusConflict, "a pet named %q already exists", p.Name)
		}
	}
	pet := req.Body
	pet.ID = s.nextID
	s.nextID++
	s.pets = append(s.pets, pet)
	for _, w := range s.watchers {
		select {
		case w <- pet:
		default: // a slow watcher misses the event rather than blocking
		}
	}
	return api.CreatePet201JSONResponse{Body: pet}, nil
}

func (s *store) GetPet(_ context.Context, req *api.GetPetRequest) (api.GetPetResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := s.index(req.PetID); i >= 0 {
		return api.GetPet200JSONResponse{Body: s.pets[i]}, nil
	}
	return nil, notFound(req.PetID)
}

func (s *store) DeletePet(_ context.Context, req *api.DeletePetRequest) (api.DeletePetResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.index(req.PetID)
	if i < 0 {
		return nil, notFound(req.PetID)
	}
	s.pets = slices.Delete(s.pets, i, i+1)
	return api.DeletePet204Response{}, nil
}

func (s *store) WatchPets(ctx context.Context, _ *api.WatchPetsRequest) (api.WatchPetsResponse, error) {
	ch := make(chan model.Pet, 16)
	s.mu.Lock()
	s.watchers = append(s.watchers, ch)
	s.mu.Unlock()
	return api.WatchPets200EventsResponse{Events: func(yield func(server.Event, error) bool) {
		defer s.unwatch(ch)
		for {
			select {
			case <-ctx.Done():
				return
			case pet := <-ch:
				ev, err := server.JSONEvent("pet", pet)
				ev.ID = fmt.Sprint(pet.ID)
				if !yield(ev, err) {
					return
				}
			}
		}
	}}, nil
}

func (s *store) unwatch(ch chan model.Pet) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.watchers = slices.DeleteFunc(s.watchers, func(c chan model.Pet) bool { return c == ch })
}

func (s *store) index(id int64) int {
	return slices.IndexFunc(s.pets, func(p model.Pet) bool { return p.ID == id })
}

func notFound(id int64) error {
	return problem.Newf(http.StatusNotFound, "no pet with id %d", id)
}
