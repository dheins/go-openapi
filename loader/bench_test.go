package loader

import "testing"

func benchmarkLoad(b *testing.B, path string) {
	for b.Loop() {
		if _, err := Load(path); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLoadPetstore32(b *testing.B) { benchmarkLoad(b, "testdata/petstore-3.2.yaml") }
func BenchmarkLoadUSPTO30(b *testing.B)    { benchmarkLoad(b, "../testdata/oai/v3.0/uspto.yaml") }
func BenchmarkLoadMultiFile(b *testing.B)  { benchmarkLoad(b, "testdata/multi/openapi.yaml") }
