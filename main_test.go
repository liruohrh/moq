package main

import (
	"reflect"
	"testing"
)

func TestParseSource(t *testing.T) {
	cases := []struct {
		name       string
		value      string
		wantSource string
		wantIfaces []string
		wantErr    bool
	}{
		{
			name:       "SingleInterface",
			value:      "./a:Foo",
			wantSource: "./a",
			wantIfaces: []string{"Foo"},
		},
		{
			name:       "MultipleInterfaces",
			value:      "./a:Foo,Bar",
			wantSource: "./a",
			wantIfaces: []string{"Foo", "Bar"},
		},
		{
			name:       "Alias",
			value:      "./a:Foo:MyFoo",
			wantSource: "./a",
			wantIfaces: []string{"Foo:MyFoo"},
		},
		{
			name:       "ImportPath",
			value:      "github.com/matryer/moq:Baz",
			wantSource: "github.com/matryer/moq",
			wantIfaces: []string{"Baz"},
		},
		{
			name:       "Spaces",
			value:      "./a: Foo , Bar ",
			wantSource: "./a",
			wantIfaces: []string{"Foo", "Bar"},
		},
		{
			name:       "WindowsDriveLetter",
			value:      `C:\src\pkg:Foo`,
			wantSource: `C:\src\pkg`,
			wantIfaces: []string{"Foo"},
		},
		{
			name:    "MissingSeparator",
			value:   "./a",
			wantErr: true,
		},
		{
			name:    "MissingSource",
			value:   ":Foo",
			wantErr: true,
		},
		{
			name:    "MissingInterface",
			value:   "./a:",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src, err := parseSource(tc.value)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error but got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSource: %s", err)
			}
			if src.Path != tc.wantSource {
				t.Errorf("source: want %q; got %q", tc.wantSource, src.Path)
			}
			if !reflect.DeepEqual(src.Interfaces, tc.wantIfaces) {
				t.Errorf("interfaces: want %v; got %v", tc.wantIfaces, src.Interfaces)
			}
		})
	}
}

func TestSourceFlagsRepeatable(t *testing.T) {
	var flags sourceFlags
	if err := flags.Set("./a:Foo"); err != nil {
		t.Fatalf("Set: %s", err)
	}
	if err := flags.Set("github.com/x/y:Bar,Baz:Quux"); err != nil {
		t.Fatalf("Set: %s", err)
	}

	want := sourceFlags{
		{Path: "./a", Interfaces: []string{"Foo"}},
		{Path: "github.com/x/y", Interfaces: []string{"Bar", "Baz:Quux"}},
	}
	if !reflect.DeepEqual(flags, want) {
		t.Errorf("want %v; got %v", want, flags)
	}

	if got := flags.String(); got == "" {
		t.Error("String should not be empty")
	}
}
