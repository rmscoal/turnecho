// Package install owns turnecho install, upgrade, and uninstall.
//
// Each operation plans first and mutates second so --dry-run prints the
// exact steps without touching the machine. On failure the command link
// and host registrations roll back in reverse order; downloaded model
// files stay as a cache for the next run.
package install

import (
	"fmt"
	"os/exec"
	"slices"
	"sort"
)

// Rollback undoes one install mutation.
type Rollback func() error

// RollbackAll runs rollbacks in reverse order, collecting every failure.
func RollbackAll(undo []Rollback) []error {
	var errs []error
	for i := len(undo) - 1; i >= 0; i-- {
		if err := undo[i](); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// Host is a supported agent host.
type Host string

// Supported hosts.
const (
	HostCodex  Host = "codex"
	HostClaude Host = "claude"
)

// Hosts lists the supported hosts in sorted order.
func Hosts() []Host {
	hosts := []Host{HostCodex, HostClaude}
	sort.Slice(hosts, func(i, j int) bool { return hosts[i] < hosts[j] })
	return hosts
}

// ParseHost validates a --host flag value.
func ParseHost(value string) (Host, error) {
	host := Host(value)
	if host != HostCodex && host != HostClaude {
		return "", fmt.Errorf("unsupported host %q: want codex or claude", value)
	}
	return host, nil
}

// DetectHosts returns the supported hosts whose CLI is on PATH.
func DetectHosts() []Host {
	var found []Host
	for _, host := range Hosts() {
		if _, err := exec.LookPath(string(host)); err == nil {
			found = append(found, host)
		}
	}
	return found
}

// ResolveHosts validates the requested host set against PATH.
// An empty request means every detected host CLI, else it is an error.
func ResolveHosts(requested []Host) ([]Host, error) {
	if len(requested) == 0 {
		detected := DetectHosts()
		if len(detected) == 0 {
			return nil, fmt.Errorf("no host CLI found on PATH: install codex or claude first")
		}
		return detected, nil
	}
	var missing []Host
	for _, host := range requested {
		if _, err := exec.LookPath(string(host)); err != nil {
			missing = append(missing, host)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("host CLI not found on PATH: %v", missing)
	}
	out := slices.Clone(requested)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}
