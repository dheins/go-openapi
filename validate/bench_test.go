package validate

import "testing"

func BenchmarkValidateUSPTO30(b *testing.B) {
	for b.Loop() {
		res, err := File("../testdata/oai/v3.0/uspto.yaml")
		if err != nil || res.HasErrors() {
			b.Fatal(err, res.Diagnostics)
		}
	}
}

func BenchmarkValidateTicTacToe31(b *testing.B) {
	for b.Loop() {
		res, err := File("../testdata/oai/v3.1/tictactoe.yaml")
		if err != nil || res.HasErrors() {
			b.Fatal(err, res.Diagnostics)
		}
	}
}
