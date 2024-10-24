module underpass

go 1.21.5

replace github.com/jackyes/underpass => ./

require (
	github.com/fatih/color v1.18.0
	github.com/gorilla/mux v1.8.1
	github.com/gorilla/websocket v1.5.3
	github.com/jackyes/underpass v0.0.0-00010101000000-000000000000
	github.com/matoous/go-nanoid/v2 v2.1.0
	github.com/spf13/cobra v1.8.1
	github.com/vmihailenco/msgpack/v5 v5.4.1
)

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	golang.org/x/sys v0.25.0 // indirect
)
