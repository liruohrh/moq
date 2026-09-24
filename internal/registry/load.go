package registry

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"
)

// srcLoadMode is the load mode required to inspect the interfaces declared
// by a source package. NeedImports and NeedDeps are required so that the
// types of the packages imported by the source are available.
const srcLoadMode = packages.NeedName | packages.NeedSyntax | packages.NeedTypes |
	packages.NeedImports | packages.NeedDeps

// LoadSource loads the source package described by spec.
//
// spec may either be a path to a directory containing Go source files or
// the import path of a Go package. Directory paths may be absolute or
// relative to fromDir (falling back to the current working directory).
//
// Import paths are resolved from fromDir so that the module's go.mod,
// including any replace directives, and the surrounding go.work workspace
// are honored. The package path reported for such a source is the module
// import path, never the local directory it may have been replaced with.
func LoadSource(spec, fromDir string) (*packages.Package, error) {
	dir, pattern := resolveSource(spec, fromDir)
	return pkgInfoFromPath(dir, pattern, srcLoadMode)
}

// PackageInfo returns the package name and the (vendor stripped) import
// path of the package contained in dir.
func PackageInfo(dir string) (name, path string, err error) {
	pkg, err := pkgInfoFromPath(dir, ".", packages.NeedName)
	if err != nil {
		return "", "", err
	}
	return pkg.Name, stripVendorPath(pkg.PkgPath), nil
}

// resolveSource decides whether spec refers to a directory on disk or to
// a package import path, and returns the directory to run the package
// load in together with the load pattern.
func resolveSource(spec, fromDir string) (dir, pattern string) {
	if isDirSource(spec, fromDir) {
		if !filepath.IsAbs(spec) {
			if fromDir != "" {
				if candidate := filepath.Join(fromDir, spec); dirExists(candidate) {
					return candidate, "."
				}
			}
			if abs, err := filepath.Abs(spec); err == nil {
				return abs, "."
			}
		}
		return spec, "."
	}

	if fromDir == "" {
		fromDir = "."
	}
	return fromDir, spec
}

// isDirSource reports whether spec should be treated as a directory path.
//
// An existing directory always wins. A missing spec which is explicitly
// path-like (absolute or starting with ".") is still treated as a
// directory so that a clear load error is reported instead of a confusing
// import path error. Everything else is treated as an import path.
func isDirSource(spec, fromDir string) bool {
	if spec == "" {
		return false
	}
	if dirExists(spec) {
		return true
	}
	if fromDir != "" && dirExists(filepath.Join(fromDir, spec)) {
		return true
	}
	return filepath.IsAbs(spec) || strings.HasPrefix(spec, ".")
}

func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

func pkgInfoFromPath(dir, pattern string, mode packages.LoadMode) (*packages.Package, error) {
	pkg, err := loadPkg(dir, pattern, mode, nil)
	if err == nil || !canIgnoreWorkspace(err) {
		return pkg, err
	}

	// The package belongs to a module which is not part of the
	// surrounding go.work workspace. Retry with the workspace disabled so
	// that moq keeps working for modules which are not listed in it.
	if retried, retryErr := loadPkg(dir, pattern, mode, envWithoutWorkspace()); retryErr == nil {
		return retried, nil
	}

	return pkg, err
}

func loadPkg(dir, pattern string, mode packages.LoadMode, env []string) (*packages.Package, error) {
	pkgs, err := packages.Load(&packages.Config{
		Mode: mode,
		Dir:  dir,
		Env:  env,
	}, pattern)
	if err != nil {
		return nil, err
	}
	if len(pkgs) == 0 {
		return nil, errors.New("package not found")
	}
	if len(pkgs) > 1 {
		return nil, errors.New("found more than one package")
	}
	if errs := pkgs[0].Errors; len(errs) != 0 {
		if len(errs) == 1 {
			return nil, errs[0]
		}
		return nil, fmt.Errorf("%s (and %d more errors)", errs[0], len(errs)-1)
	}
	return pkgs[0], nil
}

// canIgnoreWorkspace reports whether err was caused by a go.work
// workspace which does not cover the module being loaded. An explicitly
// configured workspace is always honored.
func canIgnoreWorkspace(err error) bool {
	if err == nil || !strings.Contains(err.Error(), "go.work") {
		return false
	}

	switch os.Getenv("GOWORK") {
	case "", "auto":
		return true
	default:
		return false
	}
}

// envWithoutWorkspace returns the current environment with workspace
// resolution disabled.
func envWithoutWorkspace() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GOWORK=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "GOWORK=off")
}
