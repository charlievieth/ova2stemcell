package main

import (
	"os"
	"path/filepath"
	"testing"
)

var versionTests = []struct {
	s  string
	ok bool
}{
	{"1.2", true},
	{"001.002", true},
	{"0a1.002", false},
	{"1.a", false},
	{"a1.2", false},
	{"a.2", false},
}

func TestValidateVersion(t *testing.T) {
	for _, x := range versionTests {
		err := ValidateVersion(x.s)
		if (err == nil) != x.ok {
			if x.ok {
				t.Errorf("failed to validate version: %s\n", x.s)
			} else {
				t.Errorf("expected error for version (%s) but got: %v\n", x.s, err)
			}
		}
	}
}

func TestValidateOutputDir(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateOutputDir(wd); err != nil {
		t.Error(err)
	}
	misingDir := filepath.Join("abcd,", wd, "foo", "bar")
	if err := ValidateOutputDir(misingDir); err == nil {
		t.Error(err)
	}
	filename := filepath.Join(wd, "main.go")
	if err := ValidateOutputDir(filename); err == nil {
		t.Error(err)
	}
}
