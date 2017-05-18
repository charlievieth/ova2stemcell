package main

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
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
