package install

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rmscoal/turnecho/internal/config"
	"github.com/rmscoal/turnecho/internal/tts"
)

// Plan is the ordered install work. Dry-run prints it; a real run
// executes it step by step with rollback on failure.
type Plan struct {
	Hosts      []Host
	Model      string
	ModelID    string
	ModelURL   string
	ModelDir   string
	LinkPath   string
	Executable string
	Update     bool
}

// ComputePlan resolves hosts, configuration, and filesystem targets.
// An empty host request means every detected host CLI.
func ComputePlan(requested []Host, update bool) (Plan, error) {
	hosts, err := ResolveHosts(requested)
	if err != nil {
		return Plan{}, err
	}
	cfg, err := config.Load()
	if err != nil {
		return Plan{}, err
	}
	dir, err := tts.HomeModelDir(cfg.Model)
	if err != nil {
		return Plan{}, err
	}
	exe, err := os.Executable()
	if err != nil {
		return Plan{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Plan{}, err
	}
	return Plan{
		Hosts:      hosts,
		Model:      cfg.Model,
		ModelID:    config.Models[cfg.Model],
		ModelURL:   ModelURL,
		ModelDir:   dir,
		LinkPath:   filepath.Join(home, ".local", "bin", "turnecho"),
		Executable: exe,
		Update:     update,
	}, nil
}

// Print describes the plan without changing the machine.
func (p Plan) Print(w io.Writer) {
	names := make([]string, 0, len(p.Hosts))
	for _, host := range p.Hosts {
		names = append(names, string(host))
	}
	mode := "missing model files are fetched"
	if p.Update {
		mode = "model is re-downloaded"
	}
	fmt.Fprintf(w, "Hosts: %s\n", strings.Join(names, ", "))
	fmt.Fprintf(w, "Model: %s (%s), %s\n", p.Model, p.ModelID, mode)
	fmt.Fprintf(w, "Model dir: %s\n", p.ModelDir)
	fmt.Fprintf(w, "Link: %s -> %s\n", p.LinkPath, p.Executable)
	fmt.Fprintf(w, "Plugin: %s via %s\n", PluginID, MarketplaceSource)
}
