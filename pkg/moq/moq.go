package moq

import (
	"bytes"
	"errors"
	"fmt"
	"go/token"
	"go/types"
	"io"
	"os"
	"strings"

	"github.com/matryer/moq/internal/registry"
	"github.com/matryer/moq/internal/template"
)

// Mocker can generate mock structs.
type Mocker struct {
	cfg Config

	registry *registry.Registry
	tmpl     template.Template

	groups     []sourceGroup
	pkgName    string
	moqPkgPath string
}

// Source describes one group of interfaces to mock: a single source
// package and the interfaces to generate mocks for from it.
type Source struct {
	// Path is either a path to a directory containing the source code or
	// the import path of a Go package. Import paths are resolved using
	// the module (go.mod, including replace directives) and workspace
	// (go.work) of the package that will contain the generated code, so
	// mocks can be generated for interfaces declared in dependencies.
	Path string

	// Interfaces lists the interfaces to mock from Path. Every entry is
	// either "Interface" or "Interface:MockName". When empty, the
	// interface names passed to Mock are used instead.
	Interfaces []string
}

// Config specifies details about how interfaces should be mocked.
type Config struct {
	// SrcDir is the directory containing the source code of the target
	// interfaces. It is equivalent to a single entry in Sources and is
	// kept for backwards compatibility.
	SrcDir string

	// Sources lists the groups of interfaces to mock. Multiple groups may
	// be specified, allowing interfaces declared in different packages to
	// be mocked into a single output package. When Sources is empty,
	// SrcDir is used as a single source.
	Sources []Source

	// PkgDir is the directory of the package which will contain the
	// generated code. It is used as the root for resolving sources given
	// as import paths (honoring go.mod, replace directives and go.work)
	// and to detect when a source package is the generated package
	// itself. It defaults to the current working directory.
	PkgDir string

	PkgName    string
	Formatter  string
	StubImpl   bool
	SkipEnsure bool
	WithResets bool
}

// sourceGroup is a source package together with the interfaces to mock
// from it.
type sourceGroup struct {
	src        *registry.Source
	interfaces []string
}

// New makes a new Mocker for the specified packages.
func New(cfg Config) (*Mocker, error) {
	if len(cfg.Sources) == 0 {
		if cfg.SrcDir == "" {
			return nil, errors.New("no source specified")
		}
		cfg.Sources = []Source{{Path: cfg.SrcDir}}
	}
	for _, src := range cfg.Sources {
		if src.Path == "" {
			return nil, errors.New("source path must not be empty")
		}
	}

	// Import path sources are resolved relative to the output package so
	// that its module and workspace are used. When the output directory
	// does not exist yet, fall back to the working directory.
	resolveDir := cfg.PkgDir
	if resolveDir == "" || !dirExists(resolveDir) {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		resolveDir = wd
	}

	// Best effort detection of the generated package, used both to avoid
	// importing a source which is the generated package itself and to
	// infer the package name.
	var outPkgName, outPkgPath string
	if cfg.PkgDir != "" {
		if name, path, err := registry.PackageInfo(cfg.PkgDir); err == nil {
			outPkgName, outPkgPath = name, path
		}
	}

	reg := registry.New("")
	loaded := make([]*registry.Source, len(cfg.Sources))
	for i, src := range cfg.Sources {
		pkg, err := registry.LoadSource(src.Path, resolveDir)
		if err != nil {
			return nil, fmt.Errorf("couldn't load source package %q: %s", src.Path, err)
		}
		loaded[i] = reg.AddSource(pkg)
	}

	pkgName := cfg.PkgName
	if pkgName == "" {
		switch {
		case outPkgName != "":
			pkgName = outPkgName
		case len(loaded) > 0:
			pkgName = loaded[0].Name()
		}
	}
	if pkgName == "" {
		return nil, errors.New("couldn't infer package name, use -pkg")
	}

	// The generated package is only treated as the source package when its
	// name matches, which is not the case for an external test package
	// such as "foo_test" generated next to package "foo".
	//
	// When the generated package cannot be identified, a single source
	// with no explicit package name is assumed to be the generated
	// package itself. This retains the historical behaviour.
	moqPkgPath := ""
	switch {
	case outPkgPath != "" && outPkgName == pkgName:
		moqPkgPath = outPkgPath
	case outPkgPath == "" && cfg.PkgName == "" && len(loaded) == 1:
		moqPkgPath = loaded[0].Path()
	}
	reg.SetMoqPkgPath(moqPkgPath)

	tmpl, err := template.New()
	if err != nil {
		return nil, err
	}

	groups := make([]sourceGroup, len(cfg.Sources))
	for i, src := range cfg.Sources {
		groups[i] = sourceGroup{src: loaded[i], interfaces: src.Interfaces}
	}

	return &Mocker{
		cfg:        cfg,
		registry:   reg,
		tmpl:       tmpl,
		groups:     groups,
		pkgName:    pkgName,
		moqPkgPath: moqPkgPath,
	}, nil
}

// Mock generates a mock for the specified interface name.
//
// When the Mocker was configured with Source entries that already list
// their interfaces, namePairs must be empty.
func (m *Mocker) Mock(w io.Writer, namePairs ...string) error {
	groups, err := m.resolveGroups(namePairs)
	if err != nil {
		return err
	}

	var mocks []template.MockData
	var sources []*registry.Source
	for _, g := range groups {
		for _, np := range g.interfaces {
			name, mockName := parseInterfaceName(np)
			iface, tparams, err := m.registry.LookupInterface(g.src, name)
			if err != nil {
				return err
			}

			methods := make([]template.MethodData, iface.NumMethods())
			for j := 0; j < iface.NumMethods(); j++ {
				methods[j] = m.methodData(iface.Method(j))
			}

			mocks = append(mocks, template.MockData{
				InterfaceName: name,
				MockName:      mockName,
				Methods:       methods,
				TypeParams:    m.typeParams(tparams),
			})
			sources = append(sources, g.src)
		}
	}

	data := template.Data{
		PkgName:    m.pkgName,
		Mocks:      mocks,
		StubImpl:   m.cfg.StubImpl,
		SkipEnsure: m.cfg.SkipEnsure,
		WithResets: m.cfg.WithResets,
	}

	// Import every source package which is not the generated package
	// before resolving the qualifiers, since adding an import can rename
	// an existing qualifier to avoid a conflict.
	for _, src := range sources {
		if m.samePackage(src) || m.registry.Imported(src.Path()) != nil {
			continue
		}
		if !m.cfg.SkipEnsure {
			m.registry.AddImport(src.Types())
		}
	}
	if data.MocksSomeMethod() {
		m.registry.AddImport(types.NewPackage("sync", "sync"))
	}
	for i, src := range sources {
		mocks[i].SrcPkgQualifier = m.srcPkgQualifier(src)
	}

	data.Imports = m.registry.Imports()

	var buf bytes.Buffer
	if err := m.tmpl.Execute(&buf, data); err != nil {
		return err
	}

	formatted, err := m.format(buf.Bytes())
	if err != nil {
		return err
	}

	if _, err := w.Write(formatted); err != nil {
		return err
	}
	return nil
}

// resolveGroups returns the source packages and interfaces to mock.
func (m *Mocker) resolveGroups(namePairs []string) ([]sourceGroup, error) {
	explicit := false
	for _, g := range m.groups {
		if len(g.interfaces) > 0 {
			explicit = true
			break
		}
	}

	if explicit {
		if len(namePairs) > 0 {
			return nil, errors.New("interfaces may be specified either per source or as arguments, not both")
		}

		groups := make([]sourceGroup, 0, len(m.groups))
		for _, g := range m.groups {
			if len(g.interfaces) > 0 {
				groups = append(groups, g)
			}
		}
		if len(groups) == 0 {
			return nil, errors.New("must specify one interface")
		}
		return groups, nil
	}

	if len(namePairs) == 0 {
		return nil, errors.New("must specify one interface")
	}
	if len(m.groups) != 1 {
		return nil, errors.New("must specify interfaces for each source")
	}
	return []sourceGroup{{src: m.groups[0].src, interfaces: namePairs}}, nil
}

// samePackage reports whether src is the package which will contain the
// generated code.
func (m *Mocker) samePackage(src *registry.Source) bool {
	if m.moqPkgPath != "" {
		return src.Path() == m.moqPkgPath && src.Name() == m.pkgName
	}
	return src.Name() == m.pkgName
}

// srcPkgQualifier returns the qualifier, including the trailing dot, to
// use when referring to the source package of a mock.
func (m *Mocker) srcPkgQualifier(src *registry.Source) string {
	if m.samePackage(src) {
		return ""
	}
	if imprt := m.registry.Imported(src.Path()); imprt != nil {
		return imprt.Qualifier() + "."
	}
	return src.Name() + "."
}

func (m *Mocker) typeParams(tparams *types.TypeParamList) []template.TypeParamData {
	var tpd []template.TypeParamData
	if tparams == nil {
		return tpd
	}

	tpd = make([]template.TypeParamData, tparams.Len())

	scope := m.registry.MethodScope()
	for i := 0; i < len(tpd); i++ {
		tp := tparams.At(i)
		typeParam := types.NewParam(token.Pos(i), tp.Obj().Pkg(), tp.Obj().Name(), tp.Constraint())
		tpd[i] = template.TypeParamData{
			ParamData:  template.ParamData{Var: scope.AddVar(typeParam, "")},
			Constraint: explicitConstraintType(typeParam),
		}
	}

	return tpd
}

func explicitConstraintType(typeParam *types.Var) (t types.Type) {
	underlying := typeParam.Type().Underlying().(*types.Interface)
	// check if any of the embedded types is either a basic type or a union,
	// because the generic type has to be an alias for one of those types then
	for j := 0; j < underlying.NumEmbeddeds(); j++ {
		t := underlying.EmbeddedType(j)
		switch t := t.(type) {
		case *types.Basic:
			return t
		case *types.Union: // only unions of basic types are allowed, so just take the first one as a valid type constraint
			return t.Term(0).Type()
		}
	}
	return nil
}

func (m *Mocker) methodData(f *types.Func) template.MethodData {
	sig := f.Type().(*types.Signature)

	scope := m.registry.MethodScope()
	n := sig.Params().Len()
	params := make([]template.ParamData, n)
	for i := 0; i < n; i++ {
		p := template.ParamData{
			Var: scope.AddVar(sig.Params().At(i), ""),
		}
		p.Variadic = sig.Variadic() && i == n-1 && p.Var.IsSlice() // check for final variadic argument

		params[i] = p
	}

	n = sig.Results().Len()
	results := make([]template.ParamData, n)
	for i := 0; i < n; i++ {
		results[i] = template.ParamData{
			Var: scope.AddVar(sig.Results().At(i), "Out"),
		}
	}

	return template.MethodData{
		Name:    f.Name(),
		Params:  params,
		Returns: results,
	}
}

func (m *Mocker) format(src []byte) ([]byte, error) {
	switch m.cfg.Formatter {
	case "goimports":
		return goimports(src)

	case "noop":
		return src, nil
	}

	return gofmt(src)
}

func parseInterfaceName(namePair string) (ifaceName, mockName string) {
	parts := strings.SplitN(namePair, ":", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}

	ifaceName = parts[0]
	return ifaceName, ifaceName + "Mock"
}

func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
