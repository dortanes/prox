module github.com/labostack/prox/examples/plugin-auth

go 1.25.0

require github.com/labostack/prox/sdk v0.0.0

require (
	github.com/vmihailenco/msgpack/v5 v5.4.1 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
)

replace github.com/labostack/prox/sdk => ../../sdk
