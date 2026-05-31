package doctor

import (
	"os"
	"os/exec"

	git "github.com/go-git/go-git/v5"
)

// Run is the production entry point: it fills Options' seams with the real
// implementations (exec.LookPath for tool presence, os.Getenv for the model env,
// go-git PlainOpen for the git probe — the SAME repository authority the apply
// path uses), runs Diagnose, and returns the Report. The CLI passes Root,
// ConfigPath, and SpeciesUserRoot; everything else is the real environment.
//
// PlainOpenWithOptions(DetectDotGit) walks up to find an enclosing repository,
// matching git's own behavior so doctor reports "repo" when run from a
// subdirectory of a working tree.
func Run(opts Options) Report {
	if opts.LookPath == nil {
		opts.LookPath = exec.LookPath
	}
	if opts.Getenv == nil {
		opts.Getenv = os.Getenv
	}
	if opts.OpenRepo == nil {
		opts.OpenRepo = func(root string) error {
			_, err := git.PlainOpenWithOptions(root, &git.PlainOpenOptions{DetectDotGit: true})
			return err
		}
	}
	return Diagnose(opts)
}
