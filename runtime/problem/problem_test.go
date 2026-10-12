package problem

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWrite(t *testing.T) {
	rec := httptest.NewRecorder()
	p := Newf(http.StatusConflict, "pet %d exists", 7)
	if err := Write(rec, p); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 409 || rec.Header().Get("Content-Type") != ContentType {
		t.Errorf("%d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if got := rec.Body.String(); got != `{"title":"Conflict","status":409,"detail":"pet 7 exists"}` {
		t.Errorf("body = %s", got)
	}
	if p.Error() != "409 Conflict: pet 7 exists" {
		t.Errorf("Error() = %q", p.Error())
	}
}

func TestWriteWithoutStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := Write(rec, &Problem{Title: "Oops"}); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 500 {
		t.Errorf("status = %d, want 500 for a problem without one", rec.Code)
	}
}
