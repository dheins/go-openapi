package ir

import "testing"

func TestGoName(t *testing.T) {
	tests := map[string]string{
		"pet":            "Pet",
		"petId":          "PetID",
		"pet_store-item": "PetStoreItem",
		"HTTPServer":     "HTTPServer",
		"userURLs":       "UserURLs",
		"Pet.v2":         "PetV2",
		"200Response":    "N200Response",
		"x-rate-limit":   "XRateLimit",
		"":               "Empty",
		"--":             "Empty",
		"already Good":   "AlreadyGood",
		"api_key":        "APIKey",
		"über":           "Über",
		"ids":            "IDs",
		"pet_ids":        "PetIDs",
		"urls":           "URLs",
		"bus":            "Bus",
	}
	for in, want := range tests {
		if got := GoName(in); got != want {
			t.Errorf("GoName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNamespaceClaim(t *testing.T) {
	ns := namespace{}
	for _, want := range []string{"Pet", "Pet2", "Pet3"} {
		if got := ns.claim("Pet"); got != want {
			t.Errorf("claim = %q, want %q", got, want)
		}
	}
}
