module github.com/dheins/go-openapi/contrib/zaplog

go 1.27.2

require (
	github.com/dheins/go-openapi v0.3.0
	go.uber.org/zap v1.27.0
	go.uber.org/zap/exp v0.3.0
)

require (
	github.com/dlclark/regexp2 v1.11.5 // indirect
	go.uber.org/multierr v1.10.0 // indirect
)

// Developed against the core module in this repository; consumers get the
// required version above.
replace github.com/dheins/go-openapi => ../..
