module example.com/addon-hello

go 1.26.0

require github.com/mudler/LocalAI v0.0.0

require (
	github.com/mudler/xlog v0.0.6 // indirect
	golang.org/x/net v0.60.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/term v0.46.0 // indirect
	golang.org/x/text v0.42.0 // indirect
)

// In this checkout the example builds against the LocalAI source next to it.
// A real add-on drops this line and requires a released version of LocalAI.
replace github.com/mudler/LocalAI => ../..
