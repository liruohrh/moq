package moq

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("create dir: %s", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %s", path, err)
	}
}

// TestMockMultipleSources generates mocks for interfaces declared in
// different packages into a single output package.
func TestMockMultipleSources(t *testing.T) {
	m, err := New(Config{
		PkgDir:  "testpackages/multisrc/mocks",
		PkgName: "mocks",
		Sources: []Source{
			{Path: "testpackages/multisrc/store", Interfaces: []string{"Store"}},
			{Path: "testpackages/multisrc/cache", Interfaces: []string{"Cache:CacheMock"}},
		},
	})
	if err != nil {
		t.Fatalf("moq.New: %s", err)
	}

	var buf bytes.Buffer
	if err := m.Mock(&buf); err != nil {
		t.Fatalf("m.Mock: %s", err)
	}

	strs := []string{
		"package mocks",
		`"github.com/liruohrh/moq/pkg/moq/testpackages/multisrc/cache"`,
		`"github.com/liruohrh/moq/pkg/moq/testpackages/multisrc/store"`,
		"var _ cache.Cache = &CacheMock{}",
		"var _ store.Store = &StoreMock{}",
		"SaveFunc func(ctx context.Context, item *store.Item) error",
		"GetFunc func(key string) (string, bool)",
	}
	s := buf.String()
	for _, str := range strs {
		if !strings.Contains(s, str) {
			t.Errorf("expected but missing: %q", str)
		}
	}

	if err := matchGoldenFile(
		filepath.Join("testpackages/multisrc/mocks", "mocks.golden.go"), buf.Bytes(),
	); err != nil {
		t.Errorf("check golden file: %s", err)
	}
}

// TestMockMultipleSourcesAlias ensures interfaces passed per source can
// still be renamed with the "Interface:Mock" syntax.
func TestMockMultipleSourcesAlias(t *testing.T) {
	m, err := New(Config{
		PkgDir:  "testpackages/multisrc/mocks",
		Sources: []Source{{Path: "testpackages/multisrc/store", Interfaces: []string{"Store:MyStore"}}},
	})
	if err != nil {
		t.Fatalf("moq.New: %s", err)
	}

	var buf bytes.Buffer
	if err := m.Mock(&buf); err != nil {
		t.Fatalf("m.Mock: %s", err)
	}

	s := buf.String()
	for _, str := range []string{
		"package mocks",
		"var _ store.Store = &MyStore{}",
		"type MyStore struct",
	} {
		if !strings.Contains(s, str) {
			t.Errorf("expected but missing: %q", str)
		}
	}
}

// TestMockInterfacesPerSourceAndArgumentsRejected ensures the two ways of
// specifying interfaces cannot be mixed.
func TestMockInterfacesPerSourceAndArgumentsRejected(t *testing.T) {
	m, err := New(Config{Sources: []Source{{Path: "testpackages/example", Interfaces: []string{"PersonStore"}}}})
	if err != nil {
		t.Fatalf("moq.New: %s", err)
	}

	if err := m.Mock(&bytes.Buffer{}, "PersonStore"); err == nil {
		t.Fatal("expected error but got nil")
	}
}

// TestMockMultipleSourcesSamePackageName ensures that sources whose
// packages share a name get distinct aliases in the generated import
// block.
func TestMockMultipleSourcesSamePackageName(t *testing.T) {
	m, err := New(Config{
		PkgDir: "testpackages/multisrc/mocks",
		Sources: []Source{
			{Path: "testpackages/multisrc/one", Interfaces: []string{"First"}},
			{Path: "testpackages/multisrc/two", Interfaces: []string{"Second"}},
		},
	})
	if err != nil {
		t.Fatalf("moq.New: %s", err)
	}

	var buf bytes.Buffer
	if err := m.Mock(&buf); err != nil {
		t.Fatalf("m.Mock: %s", err)
	}

	s := buf.String()
	for _, str := range []string{
		"package mocks",
		`one "github.com/liruohrh/moq/pkg/moq/testpackages/multisrc/one"`,
		`two "github.com/liruohrh/moq/pkg/moq/testpackages/multisrc/two"`,
		"var _ one.First = &FirstMock{}",
		"var _ two.Second = &SecondMock{}",
	} {
		if !strings.Contains(s, str) {
			t.Errorf("expected but missing: %q\n%s", str, s)
		}
	}
}

// TestMockExternalTestPackage ensures that generating into an external
// test package which lives next to the source package still imports and
// qualifies the source package.
func TestMockExternalTestPackage(t *testing.T) {
	m, err := New(Config{
		SrcDir:  "testpackages/example",
		PkgDir:  "testpackages/example",
		PkgName: "example_test",
	})
	if err != nil {
		t.Fatalf("moq.New: %s", err)
	}

	var buf bytes.Buffer
	if err := m.Mock(&buf, "PersonStore"); err != nil {
		t.Fatalf("m.Mock: %s", err)
	}

	s := buf.String()
	for _, str := range []string{
		"package example_test",
		`"github.com/liruohrh/moq/pkg/moq/testpackages/example"`,
		"var _ example.PersonStore = &PersonStoreMock{}",
		"GetFunc func(ctx context.Context, id string) (*example.Person, error)",
	} {
		if !strings.Contains(s, str) {
			t.Errorf("expected but missing: %q\n%s", str, s)
		}
	}
}

// TestMockMixedLocalAndDependencySources mocks an interface declared in
// the generated package itself together with one declared in a module
// dependency which is wired up with a replace directive. The dependency
// must be referenced by its import path, not by the directory it is
// replaced with.
func TestMockMixedLocalAndDependencySources(t *testing.T) {
	t.Setenv("GOWORK", "off")

	root := t.TempDir()
	depDir := filepath.Join(root, "dep")
	appDir := filepath.Join(root, "app")

	writeTestFile(t, filepath.Join(depDir, "go.mod"), "module example.com/dep\n\ngo 1.23\n")
	writeTestFile(t, filepath.Join(depDir, "cache", "cache.go"), `package cache

type Cache interface {
	Get(key string) (string, error)
}
`)

	writeTestFile(t, filepath.Join(appDir, "go.mod"), `module example.com/app

go 1.23

require example.com/dep v0.0.0

replace example.com/dep => ../dep
`)
	writeTestFile(t, filepath.Join(appDir, "service.go"), `package app

type Local interface {
	Do() error
}
`)

	m, err := New(Config{
		PkgDir:  appDir,
		PkgName: "app",
		Sources: []Source{
			{Path: appDir, Interfaces: []string{"Local"}},
			{Path: "example.com/dep/cache", Interfaces: []string{"Cache"}},
		},
	})
	if err != nil {
		t.Fatalf("moq.New: %s", err)
	}

	var buf bytes.Buffer
	if err := m.Mock(&buf); err != nil {
		t.Fatalf("m.Mock: %s", err)
	}

	s := buf.String()
	strs := []string{
		"package app",
		`"example.com/dep/cache"`,
		"var _ Local = &LocalMock{}",
		"var _ cache.Cache = &CacheMock{}",
	}
	for _, str := range strs {
		if !strings.Contains(s, str) {
			t.Errorf("expected but missing: %q\n%s", str, s)
		}
	}

	// The local source must not be imported into its own package and the
	// dependency must not be referenced by its replaced directory.
	if strings.Contains(s, `"example.com/app"`) {
		t.Errorf("generated code must not import its own package:\n%s", s)
	}
	if strings.Contains(s, depDir) {
		t.Errorf("generated code must not contain the source directory %q:\n%s", depDir, s)
	}
}

// TestMockSourceOutsideWorkspace ensures a module which is not listed in
// a surrounding go.work file can still be loaded.
func TestMockSourceOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	otherDir := filepath.Join(root, "other")
	targetDir := filepath.Join(root, "target")

	writeTestFile(t, filepath.Join(otherDir, "go.mod"), "module example.com/other\n\ngo 1.23\n")
	writeTestFile(t, filepath.Join(otherDir, "other.go"), "package other\n")
	writeTestFile(t, filepath.Join(targetDir, "go.mod"), "module example.com/target\n\ngo 1.23\n")
	writeTestFile(t, filepath.Join(targetDir, "iface.go"), `package target

type Iface interface {
	Do() error
}
`)
	writeTestFile(t, filepath.Join(root, "go.work"), `go 1.23

use ./other
`)
	// Empty GOWORK lets the go command auto-detect the workspace above.
	t.Setenv("GOWORK", "")

	m, err := New(Config{SrcDir: targetDir, PkgName: "target"})
	if err != nil {
		t.Fatalf("moq.New: %s", err)
	}

	var buf bytes.Buffer
	if err := m.Mock(&buf, "Iface"); err != nil {
		t.Fatalf("m.Mock: %s", err)
	}
	if !strings.Contains(buf.String(), "type IfaceMock struct") {
		t.Errorf("unexpected output:\n%s", buf.String())
	}
}

// TestMockImportPathSourceWithWorkspace resolves an import path source
// through a go.work workspace which replaces the need for a replace
// directive.
func TestMockImportPathSourceWithWorkspace(t *testing.T) {
	root := t.TempDir()
	depDir := filepath.Join(root, "dep")
	appDir := filepath.Join(root, "app")

	writeTestFile(t, filepath.Join(depDir, "go.mod"), "module example.com/dep\n\ngo 1.23\n")
	writeTestFile(t, filepath.Join(depDir, "cache", "cache.go"), `package cache

type Cache interface {
	Get(key string) (string, error)
}
`)

	writeTestFile(t, filepath.Join(appDir, "go.mod"), `module example.com/app

go 1.23

require example.com/dep v0.0.0
`)
	writeTestFile(t, filepath.Join(appDir, "doc.go"), "package app\n")

	writeTestFile(t, filepath.Join(root, "go.work"), `go 1.23

use (
	./app
	./dep
)
`)
	t.Setenv("GOWORK", filepath.Join(root, "go.work"))

	m, err := New(Config{
		PkgDir:  appDir,
		PkgName: "app",
		Sources: []Source{{Path: "example.com/dep/cache", Interfaces: []string{"Cache"}}},
	})
	if err != nil {
		t.Fatalf("moq.New: %s", err)
	}

	var buf bytes.Buffer
	if err := m.Mock(&buf); err != nil {
		t.Fatalf("m.Mock: %s", err)
	}

	s := buf.String()
	if !strings.Contains(s, `"example.com/dep/cache"`) {
		t.Errorf("expected dependency import path, got:\n%s", s)
	}
	if !strings.Contains(s, "var _ cache.Cache = &CacheMock{}") {
		t.Errorf("expected source package qualifier, got:\n%s", s)
	}
}
