// This file keeps ui/ (and ui/node_modules, where some npm packages ship
// Go code) out of the cauri Go module: "go test ./..." and the linters
// skip nested modules. The UI is built into internal/webui/dist.
module cauri/ui-is-not-go
