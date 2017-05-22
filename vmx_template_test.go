package main

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/charlievieth/ova2stemcell/ovftool"
)

func parseVMX(vmx string) (map[string]string, error) {
	m := make(map[string]string)
	for _, s := range strings.Split(vmx, "\n") {
		if s == "" {
			continue
		}
		n := strings.IndexByte(s, '=')
		if n == -1 {
			return nil, fmt.Errorf("parse vmx: invalid line: %s", s)
		}
		k := strings.TrimSpace(s[:n])
		v, err := strconv.Unquote(strings.TrimSpace(s[n+1:]))
		if err != nil {
			return nil, err
		}
		if _, ok := m[k]; ok {
			return nil, fmt.Errorf("parse vmx: duplicate key: %s", k)
		}
		m[k] = v
	}
	if len(m) == 0 {
		return nil, errors.New("parse vmx: empty vmx")
	}
	return m, nil
}

func TestVMXTemplate(t *testing.T) {
	const filename = "FooBarBaz.vmdk"
	const keyname = "scsi0:0.fileName"

	var buf bytes.Buffer
	if err := VMXTemplate(filename, &buf); err != nil {
		t.Fatal(err)
	}

	m, err := parseVMX(buf.String())
	if err != nil {
		t.Fatal(err)
	}
	if s := m[keyname]; s != filename {
		t.Errorf("VMXTemplate: key: %q want: %q got: %q", keyname, filename, s)
	}

	if err := VMXTemplate("", &buf); err == nil {
		t.Error("VMXTemplate: expected error for empty vmx filename")
	}
}

func extractGzipArchive(name string, t *testing.T) string {
	t.Logf("extractGzipArchive: extracting tgz: %s", name)

	tmpdir, err := ioutil.TempDir("", "test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("extractGzipArchive: using temp directory: %s", tmpdir)

	f, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	w, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := ExtractArchive(w, tmpdir); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return tmpdir
}

func TestVMXTemplateToOVF(t *testing.T) {
	const errorMsgFormat = `
TestVMXTemplateToOVF: [ovf] file (%[1]s) contains an ethernet configuration.
Using the generated [vmx] file (%[2]s), ovftool should not include any ethernet
configuration - as this leads to errors with the BOSH vSphere CPI.

Below are the generated [vmx] and [ova] files:

OVF File (%[1]s):

%[3]s

VMX File (%[2]s):

%[4]s
`

	toolpath, err := ovftool.Ovftool()
	if err != nil {
		t.Fatalf("ovftool is required to run tests: %s", err)
	}
	t.Logf("TestVMXTemplateToOVF: ovftool location: %s", toolpath)

	dirname := extractGzipArchive("testdata/patch-test.tar.gz", t)
	defer os.RemoveAll(dirname)

	vmdk := filepath.Join(dirname, "expected.vmdk")
	t.Logf("TestVMXTemplateToOVF [vmdk]: %s", vmdk)

	// make sure the vmdk exists
	if _, err := os.Stat(vmdk); err != nil {
		t.Fatal(err)
	}

	ova := filepath.Join(dirname, "test.ova")
	vmx := filepath.Join(dirname, "test.vmx")

	t.Logf("TestVMXTemplateToOVF [ova]: %s", ova)
	t.Logf("TestVMXTemplateToOVF [vmx]: %s", vmx)

	var vmxBuf bytes.Buffer
	if err := VMXTemplate("expected.vmdk", &vmxBuf); err != nil {
		t.Fatal(err)
	}
	if err := ioutil.WriteFile(vmx, vmxBuf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(toolpath, vmx, ova)
	t.Logf("TestVMXTemplateToOVF: running command: %s [%s]", cmd.Path, cmd.Args)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("error: ovftool (%s) [%s]: %s\nOutput:\n%s\n",
			toolpath, cmd.Args, err, string(out))
	}
	t.Logf("TestVMXTemplateToOVF: ovftool output:\n%s\n", string(out))

	tmpdir, err := ioutil.TempDir("", "test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpdir)
	t.Logf("TestVMXTemplateToOVF: using temp directory: %s", tmpdir)

	t.Logf("TestVMXTemplateToOVF: extracting ova (%s) to dir (%s)", ova, tmpdir)
	if err := ExtractOVA(ova, tmpdir); err != nil {
		t.Fatal(err)
	}

	ovf := filepath.Join(tmpdir, "test.ovf")
	t.Logf("TestVMXTemplateToOVF [ovf]: %s", ovf)
	b, err := ioutil.ReadFile(ovf)
	if err != nil {
		t.Fatal(err)
	}
	ovfSrc := string(b)
	if strings.Contains(ovfSrc, "ethernet") {
		t.Fatalf(errorMsgFormat, ovf, vmx, ovfSrc, vmxBuf.String())
	}
}
