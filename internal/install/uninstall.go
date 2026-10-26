package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/rmscoal/turnecho/internal/worker"
)

// RuntimeRoot returns the versioned model runtime directory.
func RuntimeRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "turnecho", "runtimes"), nil
}

// ResolvePresentHosts keeps requested hosts whose CLI exists, defaulting an
// empty request to every detected host. Absent CLIs are skipped, never an
// error: uninstall must not fail on hosts that were never installed.
func ResolvePresentHosts(requested []Host) []Host {
	if len(requested) == 0 {
		return DetectHosts()
	}
	var present []Host
	for _, host := range requested {
		if _, err := exec.LookPath(string(host)); err == nil {
			present = append(present, host)
		}
	}
	return present
}

// UninstallPlan is the read-only removal inventory. Dry-run prints it; a
// real run executes it and reports what changed.
type UninstallPlan struct {
	Hosts             []Host
	LinkPath          string
	RemoveLink        bool
	PreservedLink     string
	Runtimes          []string
	PreservedRuntimes []string
}

// PlanUninstall inspects the machine without changing it.
func PlanUninstall(requested []Host, exe string, run Runner) (UninstallPlan, error) {
	var plan UninstallPlan
	plan.Hosts = ResolvePresentHosts(requested)
	link, err := LinkPath()
	if err != nil {
		return plan, err
	}
	plan.LinkPath = link
	owned, err := OwnedLink(link, exe)
	if err != nil {
		return plan, err
	}
	if owned {
		plan.RemoveLink = true
	} else if _, statErr := os.Lstat(link); statErr == nil {
		plan.PreservedLink = link
	}
	runtimes, err := RuntimeRoot()
	if err != nil {
		return plan, err
	}
	plan.Runtimes, plan.PreservedRuntimes, err = scanRuntimes(runtimes)
	if err != nil {
		return plan, err
	}
	return plan, nil
}

// scanRuntimes splits runtime dirs into owned removals and preserved
// entries. Only dirs with a valid marker are owned; unknown entries,
// files, and unreadable dirs are preserved.
func scanRuntimes(root string) (remove, preserved []string, err error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		if !entry.IsDir() {
			preserved = append(preserved, path)
			continue
		}
		marker, merr := ReadRuntimeMarker(path)
		if merr != nil || marker.Name != RuntimeMarkerKind {
			preserved = append(preserved, path)
			continue
		}
		remove = append(remove, path)
	}
	return remove, preserved, nil
}

// UninstallReport describes what one uninstall run changed.
type UninstallReport struct {
	RemovedHosts    []Host
	RemovedLink     bool
	RemovedRuntimes []string
}

// Execute removes host registrations, then the link, then runtimes. A host
// failure stops the run before link and runtime removal so hooks never lose
// the runtime they still point at. The link and runtimes are removed under
// the worker lock with ownership re-verified, so a concurrent install cannot
// interleave and a swapped link is never deleted.
func (plan UninstallPlan) Execute(run Runner, exe string) (UninstallReport, error) {
	var report UninstallReport
	for _, host := range plan.Hosts {
		steps, err := hostSteps(host)
		if err != nil {
			return report, err
		}
		for _, step := range steps {
			done, err := step.registered(run)
			if err != nil {
				return report, fmt.Errorf("%s: %v", host, err)
			}
			if done {
				if err := step.remove(run)(); err != nil {
					return report, fmt.Errorf("%s: %v", host, err)
				}
			}
		}
		report.RemovedHosts = append(report.RemovedHosts, host)
	}
	unlock, err := worker.HoldLock()
	if err != nil {
		return report, fmt.Errorf("speech in progress, retry shortly: %v", err)
	}
	defer unlock()
	if plan.RemoveLink {
		owned, err := OwnedLink(plan.LinkPath, exe)
		if err != nil {
			return report, err
		}
		if !owned {
			return report, fmt.Errorf("link changed during uninstall, refusing to remove %s", plan.LinkPath)
		}
		if err := os.Remove(plan.LinkPath); err != nil && !os.IsNotExist(err) {
			return report, err
		}
		report.RemovedLink = true
	}
	// plan.Runtimes is the complete removal set: dirs added after planning
	// are not listed and therefore never touched here.
	for _, dir := range plan.Runtimes {
		if err := os.RemoveAll(dir); err != nil {
			return report, err
		}
		report.RemovedRuntimes = append(report.RemovedRuntimes, dir)
	}
	return report, nil
}
