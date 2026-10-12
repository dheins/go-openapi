package petserver

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func BenchmarkGetPet(b *testing.B) {
	_, h := newTestServer()
	for b.Loop() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/pets/1", nil))
		if rec.Code != 200 {
			b.Fatal(rec.Code)
		}
	}
}

func BenchmarkListPetsWithParameters(b *testing.B) {
	_, h := newTestServer()
	for b.Loop() {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/v1/pets?limit=10&tags=a,b&sort=id", nil)
		req.Header.Set("X-Request-ID", "123e4567-e89b-12d3-a456-426614174000")
		h.ServeHTTP(rec, req)
		if rec.Code != 200 {
			b.Fatal(rec.Code)
		}
	}
}

func BenchmarkCreatePet(b *testing.B) {
	_, h := newTestServer()
	for b.Loop() {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/v1/pets", strings.NewReader(`{"name":"Kit","tag":"cat"}`))
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		if rec.Code != 201 {
			b.Fatal(rec.Code, rec.Body)
		}
	}
}
