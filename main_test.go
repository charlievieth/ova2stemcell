package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

var versionTests = []struct {
	s  string
	ok bool
}{
	{"1.2", true},
	{"-1.2", false},
	{"1.-2", false},
	{"001.002", true},
	{"0a1.002", false},
	{"1.a", false},
	{"a1.2", false},
	{"a.2", false},
	{"1.2 a", false},
}

func TestValidateVersion(t *testing.T) {
	for _, x := range versionTests {
		err := validateVersion(x.s)
		if (err == nil) != x.ok {
			if x.ok {
				t.Errorf("failed to validate version: %s\n", x.s)
			} else {
				t.Errorf("expected error for version (%s) but got: %v\n", x.s, err)
			}
		}
	}
}

func readdirnames(dirname string) ([]string, error) {
	f, err := os.Open(dirname)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	names, err := f.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}

func TestExtractOVA_Valid(t *testing.T) {
	const Count = 9
	const NameFmt = "file-%d"

	tmpdir, err := ioutil.TempDir("", "test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpdir)

	if err := ExtractOVA("testdata/tar/valid.tar", tmpdir); err != nil {
		t.Fatal(err)
	}

	var expFileNames []string
	for i := 0; i <= Count; i++ {
		expFileNames = append(expFileNames, fmt.Sprintf("file-%d", i))
	}
	sort.Strings(expFileNames)

	names, err := readdirnames(tmpdir)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(expFileNames, names) {
		t.Errorf("ExtractOVA: filenames want: %v got: %v", expFileNames, names)
	}

	// the content of each file is it's index
	// and a newline so 'file-2' contains "2\n"
	validFile := func(name string) error {
		path := filepath.Join(tmpdir, name)
		b, err := ioutil.ReadFile(path)
		if err != nil {
			return err
		}
		var i int
		if _, err := fmt.Sscanf(name, NameFmt, &i); err != nil {
			return err
		}
		exp := fmt.Sprintf("%d\n", i)
		if s := string(b); s != exp {
			t.Errorf("ExtractOVA: file (%s) want: %s got: %s", name, exp, s)
		}
		return nil
	}

	for _, name := range names {
		if err := validFile(name); err != nil {
			t.Error(err)
		}
	}
}

func TestExtractOVA_Invalid(t *testing.T) {
	var tests = []struct {
		archive string
		reason  string
	}{
		{
			"has-sub-dir.tar",
			"subdirectories are not supported",
		},
		{
			"too-many-files.tar",
			"too many files read from archive (this is capped at 100)",
		},
		{
			"symlinks.tar",
			"symlinks are not supported",
		},
	}

	for _, x := range tests {
		tmpdir, err := ioutil.TempDir("", "test-")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(tmpdir)

		filename := filepath.Join("testdata", "tar", x.archive)
		if err := ExtractOVA(filename, tmpdir); err == nil {
			t.Errorf("ExtractOVA (%s): expected error because:", x.archive, x.reason)
		}
	}
}

func readFile(name string) (string, error) {
	b, err := ioutil.ReadFile(name)
	return string(b), err
}

var ovfXMLTests = []struct {
	name string
	err  error
}{
	{"full", nil},
	{"short", nil},
	{"no-ethernet-block", ErrElementNotFound},
	{"multiple-ethernet-blocks", ErrMultipleElementsFound},
}

func TestRemoveItemBlock(t *testing.T) {
	for _, x := range ovfXMLTests {
		base := fmt.Sprintf("testdata/ovf/%s", x.name)
		orig, err := readFile(base + ".orig.xml")
		if err != nil {
			t.Fatal(err)
		}
		exp, err := readFile(base + ".exp.xml")
		if err != nil {
			t.Fatal(err)
		}
		s, err := RemoveItemBlock(orig, "ethernet0")
		if err != x.err {
			t.Errorf("RemoveItemBlock (%s): want error: %v got: %v", x.name, x.err, err)
			continue
		}
		if s != exp {
			t.Fatalf("RemoveItemBlock (%s):\n"+
				"--- WANT BEGIN ---\n%s\n---WANT END ---\n\n"+
				"--- GOT BEGIN ---\n%s\n---GOT END ---\n",
				x.name, exp, s)
		}
	}
}
