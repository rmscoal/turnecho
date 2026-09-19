// Command turnecho is the TurnEcho voice companion for coding agents.
package main

import "fmt"

// version is overridden at release time via ldflags.
var version = "development"

func main() {
	fmt.Printf("turnecho %s\n", version)
}
