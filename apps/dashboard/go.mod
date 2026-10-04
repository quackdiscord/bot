module github.com/quackdiscord/bot/dashboard

go 1.27.1

// The JavaScript toolchain lives next to the Go server; keep go ./... out of it.
ignore ./node_modules
