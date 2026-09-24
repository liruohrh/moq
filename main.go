package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/liruohrh/moq/pkg/moq"
)

// Version is the command version, injected at build time.
var Version string = "dev"

type userFlags struct {
	outFile    string
	pkgName    string
	formatter  string
	stubImpl   bool
	skipEnsure bool
	withResets bool
	remove     bool
	sources    sourceFlags
	args       []string
}

// sourceFlags implements flag.Value so that -source may be repeated, each
// occurrence describing one group of interfaces to mock.
type sourceFlags []moq.Source

// String returns a human readable representation of the sources.
func (s *sourceFlags) String() string {
	if s == nil {
		return ""
	}

	parts := make([]string, 0, len(*s))
	for _, src := range *s {
		parts = append(parts, src.Path+":"+strings.Join(src.Interfaces, ","))
	}
	return strings.Join(parts, " ")
}

// Set parses a single -source value of the form
// "<source>:<interface>[,<interface>...]" where every interface is either
// "Interface" or "Interface:MockName".
func (s *sourceFlags) Set(value string) error {
	src, err := parseSource(value)
	if err != nil {
		return err
	}

	*s = append(*s, src)
	return nil
}

func main() {
	var flags userFlags
	flag.StringVar(&flags.outFile, "out", "", "output file (default stdout)")
	flag.StringVar(&flags.pkgName, "pkg", "", "package name (default will infer)")
	flag.StringVar(&flags.formatter, "fmt", "", "go pretty-printer: gofmt, goimports or noop (default gofmt)")
	flag.BoolVar(&flags.stubImpl, "stub", false,
		"return zero values when no mock implementation is provided, do not panic")
	printVersion := flag.Bool("version", false, "show the version for moq")
	flag.BoolVar(&flags.skipEnsure, "skip-ensure", false,
		"suppress mock implementation check, avoid import cycle if mocks generated outside of the tested package")
	flag.BoolVar(&flags.remove, "rm", false, "first remove output file, if it exists")
	flag.BoolVar(&flags.withResets, "with-resets", false,
		"generate functions to facilitate resetting calls made to a mock")
	flag.Var(&flags.sources, "source",
		"source package and interfaces to mock, repeatable, format: <dir-or-import-path>:<interface>[:alias][,<interface>...]")

	flag.Usage = func() {
		fmt.Println(`moq [flags] source-dir interface [interface2 [interface3 [...]]]`)
		fmt.Println(`moq [flags] -source <dir-or-import-path>:<interface>[:alias][,<interface>...] [-source ...]`)
		flag.PrintDefaults()
		fmt.Println(`Specifying an alias for the mock is also supported with the format 'interface:alias'`)
		fmt.Println(`Ex: moq -pkg different . MyInterface:MyMock`)
		fmt.Println(`Ex: moq -out mocks.go -source ./a:Foo,Bar -source github.com/you/dep:Baz:DepMock`)
	}

	flag.Parse()
	flags.args = flag.Args()

	if *printVersion {
		fmt.Printf("moq version %s\n", Version)
		os.Exit(0)
	}

	if err := run(flags); err != nil {
		fmt.Fprintln(os.Stderr, err)
		flag.Usage()
		os.Exit(1)
	}
}

func run(flags userFlags) error {
	if len(flags.sources) > 0 && len(flags.args) > 0 {
		return errors.New("-source cannot be combined with positional source/interface arguments")
	}
	if len(flags.sources) == 0 && len(flags.args) < 2 {
		return errors.New("not enough arguments")
	}

	if flags.remove && flags.outFile != "" {
		if err := os.Remove(flags.outFile); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}

	var buf bytes.Buffer
	var out io.Writer = os.Stdout
	pkgDir := ""
	if flags.outFile != "" {
		out = &buf
		// The directory of the output file is the root used to resolve
		// source packages given as import paths.
		pkgDir = filepath.Dir(flags.outFile)
	}

	cfg := moq.Config{
		PkgDir:     pkgDir,
		PkgName:    flags.pkgName,
		Formatter:  flags.formatter,
		StubImpl:   flags.stubImpl,
		SkipEnsure: flags.skipEnsure,
		WithResets: flags.withResets,
	}

	var interfaceArgs []string
	if len(flags.sources) > 0 {
		cfg.Sources = flags.sources
	} else {
		cfg.SrcDir = flags.args[0]
		interfaceArgs = flags.args[1:]
	}

	m, err := moq.New(cfg)
	if err != nil {
		return err
	}

	if err = m.Mock(out, interfaceArgs...); err != nil {
		return err
	}

	if flags.outFile == "" {
		return nil
	}

	// create the file
	err = os.MkdirAll(filepath.Dir(flags.outFile), 0o750)
	if err != nil {
		return err
	}

	return os.WriteFile(flags.outFile, buf.Bytes(), 0o600)
}

// parseSource parses a -source flag value.
func parseSource(value string) (moq.Source, error) {
	src, list, err := splitSource(value)
	if err != nil {
		return moq.Source{}, err
	}

	var interfaces []string
	for _, name := range strings.Split(list, ",") {
		name = strings.TrimSpace(name)
		if name != "" {
			interfaces = append(interfaces, name)
		}
	}

	if src == "" {
		return moq.Source{}, fmt.Errorf("invalid -source %q: missing source", value)
	}
	if len(interfaces) == 0 {
		return moq.Source{}, fmt.Errorf("invalid -source %q: missing interface", value)
	}

	return moq.Source{Path: src, Interfaces: interfaces}, nil
}

// splitSource splits a -source value of the form
// "<source>:<interface>[,<interface>...]" into the source and the list of
// interfaces.
func splitSource(value string) (source, interfaces string, err error) {
	start := 0
	if hasDriveLetter(value) {
		// Do not treat the colon of a Windows drive letter as the
		// separator.
		start = 2
	}

	idx := strings.IndexByte(value[start:], ':')
	if idx < 0 {
		return "", "", fmt.Errorf("invalid -source %q: expected <source>:<interface>[,<interface>...]", value)
	}
	idx += start

	return value[:idx], value[idx+1:], nil
}

func hasDriveLetter(value string) bool {
	if len(value) < 3 {
		return false
	}

	c := value[0]
	if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
		return false
	}

	return value[1] == ':' && (value[2] == '\\' || value[2] == '/')
}
