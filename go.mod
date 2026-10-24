module github.com/laix/layx-reader

go 1.27.2

tool github.com/egoist/mygo/cmd/mygo

require (
	github.com/egoist/mygo v0.6.3
	github.com/go-text/typesetting v0.3.5
	github.com/laix/layx v0.0.0
	github.com/laix/layx/epub v0.0.0
	golang.org/x/net v0.59.0
	golang.org/x/text v0.43.0
)

require (
	github.com/ebitengine/purego v0.11.1 // indirect
	golang.org/x/image v0.46.0 // indirect
)

replace (
	github.com/laix/layx => /home/laix/layx
	github.com/laix/layx/epub => /home/laix/layx/epub
)
