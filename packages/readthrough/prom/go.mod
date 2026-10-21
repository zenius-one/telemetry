module github.com/zenius-one/telemetry/packages/readthrough/prom

go 1.27.1

require (
	github.com/prometheus/client_golang v1.25.0
	github.com/zenius-one/cachestore/packages/cachestore-go v0.1.0
	github.com/zenius-one/telemetry/packages/readthrough v0.0.0
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/prometheus/client_model v0.6.3 // indirect
	github.com/prometheus/common v0.72.0 // indirect
	github.com/prometheus/procfs v0.22.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)

// Bootstrap only: core has no release tag yet, and Go cannot resolve a required version without
// one. Delete this line and pin the released core (GOWORK=off go get
// github.com/zenius-one/telemetry/packages/readthrough@vX.Y.Z) in the first follow-up after core
// is tagged. See .ai/references/dependency-graph/README.md.
replace github.com/zenius-one/telemetry/packages/readthrough => ../
