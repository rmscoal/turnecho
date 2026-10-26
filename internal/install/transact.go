package install

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// UpgradePlan is the ordered upgrade. Dry-run prints it; a real run
// executes it with rollback on failure.
type UpgradePlan struct {
	Target     string
	Bundle     ReleaseAsset
	Sums       ReleaseAsset
	ReleaseDir string
	LinkPath   string
}

// CheckForUpgrade reports whether release is newer than current. Equal and
// older are not errors; the caller decides how to report them.
func CheckForUpgrade(current string, release Release) (bool, error) {
	cmp, err := CompareVersions(current, release.Tag)
	if err != nil {
		return false, err
	}
	return cmp == 1, nil
}

// PlanUpgrade resolves the release assets and filesystem targets. It
// refuses anything that is not strictly newer than current.
func PlanUpgrade(current string, release Release) (UpgradePlan, error) {
	newer, err := CheckForUpgrade(current, release)
	if err != nil {
		return UpgradePlan{}, err
	}
	if !newer {
		return UpgradePlan{}, fmt.Errorf("release %s is not newer than %s", release.Tag, current)
	}
	bundle, sums, err := SelectAsset(release)
	if err != nil {
		return UpgradePlan{}, err
	}
	root, err := ReleaseRoot()
	if err != nil {
		return UpgradePlan{}, err
	}
	link, err := LinkPath()
	if err != nil {
		return UpgradePlan{}, err
	}
	return UpgradePlan{
		Target:     release.Tag,
		Bundle:     bundle,
		Sums:       sums,
		ReleaseDir: filepath.Join(root, release.Tag),
		LinkPath:   link,
	}, nil
}

// Print describes the upgrade without changing the machine.
func (p UpgradePlan) Print(w io.Writer) {
	fmt.Fprintf(w, "Target: %s\n", p.Target)
	fmt.Fprintf(w, "Bundle: %s\n", p.Bundle.URL)
	fmt.Fprintf(w, "Checksums: %s\n", p.Sums.URL)
	fmt.Fprintf(w, "Release dir: %s\n", p.ReleaseDir)
	fmt.Fprintf(w, "Link: %s\n", p.LinkPath)
	fmt.Fprintf(w, "Model: pinned by the release manifest\n")
}

// UpgradeDeps injects process boundaries for tests.
type UpgradeDeps struct {
	Selfcheck func(bin, modelDir, out string) error
	Lock      func() (func(), error)
}

// RunUpgrade executes the plan: prepare everything lock-free, then hold one
// worker lock across revalidation, activation, and commit or rollback.
func RunUpgrade(plan UpgradePlan, deps UpgradeDeps) error {
	stagedRelease, discardStagedRelease, err := StageRelease(plan.Bundle, plan.Sums, os.TempDir())
	if err != nil {
		return err
	}
	// Runs even after a successful activation: the staged dir has moved by
	// then, and this removes the leftover staging area instead.
	defer discardStagedRelease()
	manifest, err := ReadReleaseManifest(stagedRelease)
	if err != nil {
		return err
	}
	if manifest.Version != plan.Target {
		return fmt.Errorf("bundle reports %s, wanted %s", manifest.Version, plan.Target)
	}
	modelDir, err := ModelDirForID(manifest.Model.ID)
	if err != nil {
		return err
	}
	start := ReadLiveState(plan.LinkPath, modelDir)

	var stagedModel string
	if !ModelReady(modelDir, manifest.Model.SHA256) {
		var discardStagedModel func()
		stagedModel, discardStagedModel, err = StageModel(manifest.Model.URL, manifest.Model.ID, manifest.Model.SHA256, filepath.Dir(modelDir))
		if err != nil {
			return err
		}
		// Runs even after a successful activation: the staged dir has moved
		// by then, and this removes the leftover staging area instead.
		defer discardStagedModel()
	}

	selfcheckOut, err := os.CreateTemp("", ".turnecho-selfcheck-*.wav")
	if err != nil {
		return err
	}
	selfcheckWAV := selfcheckOut.Name()
	selfcheckOut.Close()
	defer os.Remove(selfcheckWAV)
	checkModel := stagedModel
	if checkModel == "" {
		checkModel = modelDir
	}
	if err := deps.Selfcheck(ReleaseExe(stagedRelease), checkModel, selfcheckWAV); err != nil {
		return fmt.Errorf("staged release failed its check: %v", err)
	}
	if err := ValidateWAV(selfcheckWAV); err != nil {
		return err
	}

	unlock, err := deps.Lock()
	if err != nil {
		return fmt.Errorf("speech in progress, retry shortly: %v", err)
	}
	defer unlock()

	if now := ReadLiveState(plan.LinkPath, modelDir); now != start {
		return fmt.Errorf("installation changed during upgrade, retry to re-plan")
	}

	var undo []Rollback
	failed := func(err error) error {
		if errs := RollbackAll(undo); len(errs) > 0 {
			return fmt.Errorf("%v; rollback: %v", err, errors.Join(errs...))
		}
		return err
	}
	var modelCommit func() error
	if stagedModel != "" {
		var undoModel Rollback
		modelCommit, undoModel, err = ActivateModel(stagedModel, modelDir)
		if err != nil {
			return err
		}
		undo = append(undo, undoModel)
	}
	releaseDir, err := PrepareReleaseDir(stagedRelease, plan.Target)
	if err != nil {
		return failed(err)
	}
	previousLink, err := currentLinkTarget(plan.LinkPath)
	if err != nil {
		return failed(err)
	}
	undoLink, err := EnsureLink(plan.LinkPath, ReleaseExe(releaseDir))
	if err != nil {
		return failed(err)
	}
	undo = append(undo, undoLink)

	if modelCommit != nil {
		if err := modelCommit(); err != nil {
			return failed(err)
		}
	}
	if err := prunePreviousRelease(previousLink, releaseDir); err != nil {
		return failed(err)
	}
	return nil
}

// currentLinkTarget resolves the managed link, tolerating its absence.
func currentLinkTarget(linkPath string) (string, error) {
	target, err := filepath.EvalSymlinks(linkPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return target, nil
}

// prunePreviousRelease removes the superseded release unit after a
// successful swap. Foreign or dev-install targets are left alone.
func prunePreviousRelease(previousLink, releaseDir string) error {
	if previousLink == "" || previousLink == ReleaseExe(releaseDir) {
		return nil
	}
	unit := filepath.Dir(filepath.Dir(previousLink))
	manifest, err := ReadReleaseManifest(unit)
	if err != nil || manifest.Name != ReleaseManifestKind {
		return nil
	}
	releases, err := ReleaseRoot()
	if err != nil {
		return err
	}
	// Compare resolved paths: HOME may itself sit behind a symlink, so
	// every side is resolved before comparing.
	if resolved, rerr := filepath.EvalSymlinks(releases); rerr == nil {
		releases = resolved
	}
	unitDir := unit
	if resolved, rerr := filepath.EvalSymlinks(unitDir); rerr == nil {
		unitDir = resolved
	}
	want := releaseDir
	if resolved, rerr := filepath.EvalSymlinks(releaseDir); rerr == nil {
		want = resolved
	}
	if filepath.Dir(unitDir) != releases || unitDir == want {
		return nil
	}
	return os.RemoveAll(unit)
}
