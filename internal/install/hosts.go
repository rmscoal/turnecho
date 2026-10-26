package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Marketplace constants shared by both hosts.
const (
	// MarketplaceName is the turnecho marketplace on each host.
	MarketplaceName = "turnecho"
	// MarketplaceSource is the git source both hosts register.
	MarketplaceSource = "rmscoal/turnecho"
	// PluginID is the installed plugin selector.
	PluginID = "turnecho@turnecho"
)

// Runner executes one host CLI command, returning stdout.
// Tests replace it; the default shells out to the real CLI.
type Runner func(name string, args ...string) (string, error)

// DefaultRunner is the production host CLI runner.
func DefaultRunner(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			detail := strings.TrimSpace(string(exit.Stderr))
			if detail == "" {
				detail = err.Error()
			}
			return "", fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), detail)
		}
		return "", err
	}
	return string(out), nil
}

// EnsureHost registers the marketplace and plugin on one host, skipping
// steps that are already done. The returned rollback undoes only the steps
// this call changed, in reverse order. On failure this call rolls back its
// own partial work before returning the error.
func EnsureHost(host Host, run Runner) (Rollback, error) {
	var undo []Rollback
	failed := func(err error) (Rollback, error) {
		if errs := RollbackAll(undo); len(errs) > 0 {
			err = fmt.Errorf("%v; rollback: %v", err, errors.Join(errs...))
		}
		return nil, err
	}
	steps, err := hostSteps(host)
	if err != nil {
		return nil, err
	}
	for _, step := range steps {
		done, err := step.registered(run)
		if err != nil {
			return failed(err)
		}
		if done {
			continue
		}
		if err := step.add(run); err != nil {
			return failed(err)
		}
		undo = append(undo, step.remove(run))
	}
	combined := append([]Rollback(nil), undo...)
	return func() error { return errors.Join(RollbackAll(combined)...) }, nil
}

// hostStep is one registration: check, add, and remove the check's subject.
type hostStep struct {
	registered func(Runner) (bool, error)
	add        func(Runner) error
	remove     func(Runner) Rollback
}

func hostSteps(host Host) ([]hostStep, error) {
	switch host {
	case HostCodex:
		return []hostStep{
			{
				registered: func(run Runner) (bool, error) {
					return listed(run, MarketplaceName, codexMarketNames, "codex", "plugin", "marketplace", "list", "--json")
				},
				add: func(run Runner) error {
					_, err := run("codex", "plugin", "marketplace", "add", MarketplaceSource)
					return err
				},
				remove: func(run Runner) Rollback {
					return func() error {
						_, err := run("codex", "plugin", "marketplace", "remove", MarketplaceName)
						return err
					}
				},
			},
			{
				registered: func(run Runner) (bool, error) {
					return listed(run, PluginID, codexPluginIDs, "codex", "plugin", "list", "--json")
				},
				add: func(run Runner) error {
					_, err := run("codex", "plugin", "add", PluginID, "--json")
					return err
				},
				remove: func(run Runner) Rollback {
					return func() error {
						_, err := run("codex", "plugin", "remove", PluginID)
						return err
					}
				},
			},
		}, nil
	case HostClaude:
		return []hostStep{
			{
				registered: func(run Runner) (bool, error) {
					return listed(run, MarketplaceName, claudeMarketNames, "claude", "plugin", "marketplace", "list", "--json")
				},
				add: func(run Runner) error {
					_, err := run("claude", "plugin", "marketplace", "add", MarketplaceSource, "--scope", "user", "--json")
					return err
				},
				remove: func(run Runner) Rollback {
					return func() error {
						_, err := run("claude", "plugin", "marketplace", "remove", MarketplaceName, "--scope", "user", "--json")
						return err
					}
				},
			},
			{
				registered: func(run Runner) (bool, error) {
					return listed(run, PluginID, claudePluginIDs, "claude", "plugin", "list", "--json")
				},
				add: func(run Runner) error {
					_, err := run("claude", "plugin", "install", PluginID, "--scope", "user", "-y", "--json")
					return err
				},
				remove: func(run Runner) Rollback {
					return func() error {
						_, err := run("claude", "plugin", "uninstall", PluginID, "--scope", "user", "--json")
						return err
					}
				},
			},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported host %q", host)
	}
}

// listed runs a JSON list command and reports whether want appears.
func listed(run Runner, want string, names func(string) ([]string, error), name string, args ...string) (bool, error) {
	out, err := run(name, args...)
	if err != nil {
		return false, err
	}
	got, err := names(out)
	if err != nil {
		return false, err
	}
	for _, name := range got {
		if name == want {
			return true, nil
		}
	}
	return false, nil
}

func codexMarketNames(out string) ([]string, error) {
	var listed struct {
		Marketplaces []struct {
			Name string `json:"name"`
		} `json:"marketplaces"`
	}
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		return nil, fmt.Errorf("parsing codex marketplaces: %w", err)
	}
	var names []string
	for _, market := range listed.Marketplaces {
		names = append(names, market.Name)
	}
	return names, nil
}

func codexPluginIDs(out string) ([]string, error) {
	var listed struct {
		Installed []struct {
			PluginID string `json:"pluginId"`
		} `json:"installed"`
	}
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		return nil, fmt.Errorf("parsing codex plugins: %w", err)
	}
	var ids []string
	for _, plugin := range listed.Installed {
		ids = append(ids, plugin.PluginID)
	}
	return ids, nil
}

func claudeMarketNames(out string) ([]string, error) {
	var listed []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		return nil, fmt.Errorf("parsing claude marketplaces: %w", err)
	}
	var names []string
	for _, market := range listed {
		names = append(names, market.Name)
	}
	return names, nil
}

func claudePluginIDs(out string) ([]string, error) {
	var listed []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		return nil, fmt.Errorf("parsing claude plugins: %w", err)
	}
	var ids []string
	for _, plugin := range listed {
		ids = append(ids, plugin.ID)
	}
	return ids, nil
}
