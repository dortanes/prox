// Example plugin that provides a static list of targets.
//
// This demonstrates the prox plugin protocol:
//   1. Read "configure" from stdin
//   2. Push "set_targets" to stdout
//   3. Periodically refresh (simulating dynamic discovery)
//
// Build:
//
//	go build -o resolver ./examples/plugin-static
//
// Config:
//
//	{
//	  match: { domain: "*.**", path: "/api/*" },
//	  plugins: ["./resolver"],
//	  balancer: { type: "leastconn", targets: [] },
//	  action: { type: "proxy", upstream: "{target}" },
//	}
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// --- Protocol types ---

type Request struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type ConfigureParams struct {
	RouteID string `json:"route_id"`
	Match   struct {
		Domain string `json:"domain"`
		Path   string `json:"path"`
	} `json:"match"`
}

type SetTargets struct {
	Method string          `json:"method"`
	Params SetTargetParams `json:"params"`
}

type SetTargetParams struct {
	RouteID string   `json:"route_id"`
	Targets []string `json:"targets"`
}

// --- Plugin state ---

var (
	mu     sync.Mutex
	stdout = json.NewEncoder(os.Stdout)
)

func send(msg interface{}) {
	mu.Lock()
	defer mu.Unlock()
	_ = stdout.Encode(msg)
}

func main() {
	log.SetOutput(os.Stderr)
	log.SetPrefix("[plugin] ")

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			log.Printf("invalid message: %v", err)
			continue
		}

		switch req.Method {
		case "configure":
			handleConfigure(req.Params)
		default:
			log.Printf("unknown method: %s", req.Method)
		}
	}
}

func handleConfigure(raw json.RawMessage) {
	var params ConfigureParams
	if err := json.Unmarshal(raw, &params); err != nil {
		log.Printf("bad configure params: %v", err)
		return
	}

	log.Printf("configured for route %s (domain=%s, path=%s)",
		params.RouteID, params.Match.Domain, params.Match.Path)

	// Acknowledge.
	fmt.Println(`{"result":"ok"}`)

	// Start a discovery loop for this route.
	go discoveryLoop(params)
}

// discoveryLoop simulates periodic target discovery.
// In a real plugin, this would query a service registry, DNS, mesh API, etc.
func discoveryLoop(params ConfigureParams) {
	// Extract the first wildcard segment from the domain pattern
	// to use as a service filter. E.g. "*.**" means "use the first
	// subdomain label as the service name".
	log.Printf("starting discovery for route %s", params.RouteID)

	for {
		targets := discover(params.Match.Domain)

		send(SetTargets{
			Method: "set_targets",
			Params: SetTargetParams{
				RouteID: params.RouteID,
				Targets: targets,
			},
		})

		log.Printf("pushed %d targets for route %s", len(targets), params.RouteID)

		// Re-discover every 30 seconds.
		time.Sleep(30 * time.Second)
	}
}

// discover returns a list of upstream addresses.
// Replace this with your actual discovery logic.
func discover(domainPattern string) []string {
	// Example: map domain patterns to known backends.
	_ = strings.Split(domainPattern, ".")

	return []string{
		"10.0.1.1:8080",
		"10.0.1.2:8080",
		"10.0.1.3:8080",
	}
}
