package registry

import (
	"fmt"
	"go/ast"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// Registry encapsulates types information for the source and mock
// destination package. For the mock package, it tracks the list of
// imports and ensures there are no conflicts in the imported package
// qualifiers.
//
// A single Registry may hold several source packages so that mocks for
// interfaces declared in more than one package can be generated into a
// single output package.
type Registry struct {
	moqPkgPath string
	aliases    map[string]string
	imports    map[string]*Package
	sources    map[string]*Source
}

// Source is a package from which interfaces are mocked.
type Source struct {
	name string
	path string
	pkg  *types.Package
}

// Name returns the name of the source package.
func (s Source) Name() string { return s.name }

// Path is the full package import path (without vendor).
func (s Source) Path() string { return s.path }

// Types returns the types information for the source package.
func (s Source) Types() *types.Package { return s.pkg }

// New returns a new instance of Registry which will generate mocks for
// the package with the given import path. moqPkgPath may be empty when
// the destination package is not known.
func New(moqPkgPath string) *Registry {
	return &Registry{
		moqPkgPath: stripVendorPath(moqPkgPath),
		aliases:    make(map[string]string),
		imports:    make(map[string]*Package),
		sources:    make(map[string]*Source),
	}
}

// MoqPkgPath returns the import path of the package which will contain
// the generated mocks. It may be empty.
func (r Registry) MoqPkgPath() string { return r.moqPkgPath }

// SetMoqPkgPath sets the import path of the package which will contain
// the generated mocks.
func (r *Registry) SetMoqPkgPath(path string) { r.moqPkgPath = stripVendorPath(path) }

// AddSource registers a loaded source package. It is safe to register
// the same package more than once; the existing Source is returned.
func (r *Registry) AddSource(pkg *packages.Package) *Source {
	path := stripVendorPath(pkg.PkgPath)
	if src, ok := r.sources[path]; ok {
		return src
	}

	src := &Source{name: pkg.Name, path: path, pkg: pkg.Types}
	r.sources[path] = src

	// Aliases declared by the source files are reused in the generated
	// code. When multiple source packages alias the same import
	// differently, the first one seen wins.
	for importPath, alias := range parseImportsAliases(pkg.Syntax) {
		if _, ok := r.aliases[importPath]; !ok {
			r.aliases[importPath] = alias
		}
	}

	return src
}

// Sources returns the registered source packages sorted by import path.
func (r Registry) Sources() []*Source {
	sources := make([]*Source, 0, len(r.sources))
	for _, src := range r.sources {
		sources = append(sources, src)
	}
	sort.Slice(sources, func(i, j int) bool {
		return sources[i].path < sources[j].path
	})
	return sources
}

// LookupInterface returns the underlying interface definition of the
// given interface name in the provided source package.
func (r Registry) LookupInterface(src *Source, name string) (*types.Interface, *types.TypeParamList, error) {
	if src == nil || src.pkg == nil {
		return nil, nil, fmt.Errorf("source package not loaded")
	}

	obj := src.pkg.Scope().Lookup(name)
	if obj == nil {
		return nil, nil, fmt.Errorf("interface not found: %s", name)
	}

	if !types.IsInterface(obj.Type()) {
		return nil, nil, fmt.Errorf("%s (%s) is not an interface", name, obj.Type())
	}

	var tparams *types.TypeParamList
	named, ok := obj.Type().(*types.Named)
	if ok {
		tparams = named.TypeParams()
	}

	return obj.Type().Underlying().(*types.Interface).Complete(), tparams, nil
}

// MethodScope returns a new MethodScope.
func (r *Registry) MethodScope() *MethodScope {
	return &MethodScope{
		registry:   r,
		moqPkgPath: r.moqPkgPath,
		conflicted: map[string]bool{},
	}
}

// AddImport adds the given package to the set of imports. It generates a
// suitable alias if there are any conflicts with previously imported
// packages.
func (r *Registry) AddImport(pkg *types.Package) *Package {
	path := stripVendorPath(pkg.Path())
	if path == r.moqPkgPath {
		return nil
	}

	if imprt, ok := r.imports[path]; ok {
		return imprt
	}

	imprt := Package{pkg: pkg, Alias: r.aliases[path]}

	if conflict, ok := r.searchImport(imprt.Qualifier()); ok {
		r.resolveImportConflict(&imprt, conflict, 0)
	}

	r.imports[path] = &imprt
	return &imprt
}

// Imported returns the already registered import for the given package
// path, or nil if it has not been imported.
func (r Registry) Imported(path string) *Package {
	return r.imports[stripVendorPath(path)]
}

// Imports returns the list of imported packages. The list is sorted by
// path.
func (r Registry) Imports() []*Package {
	imports := make([]*Package, 0, len(r.imports))
	for _, imprt := range r.imports {
		imports = append(imports, imprt)
	}
	sort.Slice(imports, func(i, j int) bool {
		return imports[i].Path() < imports[j].Path()
	})
	return imports
}

func (r Registry) searchImport(name string) (*Package, bool) {
	for _, imprt := range r.imports {
		if imprt.Qualifier() == name {
			return imprt, true
		}
	}

	return nil, false
}

// resolveImportConflict generates and assigns a unique alias for
// packages with conflicting qualifiers.
func (r Registry) resolveImportConflict(a, b *Package, lvl int) {
	if a.uniqueName(lvl) == b.uniqueName(lvl) {
		r.resolveImportConflict(a, b, lvl+1)
		return
	}

	for _, p := range []*Package{a, b} {
		name := p.uniqueName(lvl)
		// Even though the name is not conflicting with the other package we
		// got, the new name we want to pick might already be taken. So check
		// again for conflicts and resolve them as well. Since the name for
		// this package would also get set in the recursive function call, skip
		// setting the alias after it.
		if conflict, ok := r.searchImport(name); ok && conflict != p {
			r.resolveImportConflict(p, conflict, lvl+1)
			continue
		}

		p.Alias = name
	}
}

func parseImportsAliases(syntaxTree []*ast.File) map[string]string {
	aliases := make(map[string]string)
	for _, syntax := range syntaxTree {
		for _, imprt := range syntax.Imports {
			if imprt.Name != nil && imprt.Name.Name != "." && imprt.Name.Name != "_" {
				aliases[strings.Trim(imprt.Path.Value, `"`)] = imprt.Name.Name
			}
		}
	}
	return aliases
}
