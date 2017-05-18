package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha1"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/charlievieth/ova2stemcell/rdiff"
)

var (
	Version     string
	OutputDir   string
	EnableDebug bool
	OvaFile     string
	OvfDir      string
	VHDFile     string
	DeltaFile   string
)

var Debugf = func(format string, a ...interface{}) {}

const UsageMessage = `
Usage %[1]s: [OPTIONS...] [-VERSION version] [-OVA FILENAME] [-OVF DIRNAME]

Creates a BOSH stemcell from a OVA file or a directory containing an OVF
package.

Usage:
  Either the [ova] or [ovf] flag must be specified, the [version] flag
  is required.  If the [output] flag is not specified the stemcell fill
  will be created in the current working directory.

Examples:
  %[1]s -v 1.2 -ova vm.ova
  %[1]s -v 1.2 -ovf ~/dirname/ -o ~/stemcells/

Flags:
`

func init() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, UsageMessage, filepath.Base(os.Args[0]))
		flag.PrintDefaults()
	}

	flag.StringVar(&VHDFile, "vhd", "", "Path to initial VHD file to patch")

	flag.StringVar(&DeltaFile, "delta", "", "Path to deltafile that will be applied to the VHD")
	flag.StringVar(&DeltaFile, "d", "", "Deltafile path (shorthand)")

	flag.StringVar(&Version, "version", "", "Stemcell version in the form of [DIGITS].[DIGITS] (e.x. 123.01)")
	flag.StringVar(&Version, "v", "", "Stemcell version (shorthand)")

	flag.StringVar(&OutputDir, "output", "",
		"Output directory, default is the current working directory.")
	flag.StringVar(&OutputDir, "o", "", "Output directory (shorthand)")

	flag.BoolVar(&EnableDebug, "debug", false, "Print lots of debugging information")
}

func Usage() {
	flag.Usage()
	os.Exit(1)
}

func validFile(name string) error {
	fi, err := os.Stat(name)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("not a regular file: %s", name)
	}
	return nil
}

func ValidateInputFlags(vhd, delta string) error {
	Debugf("validating [vhd] (%s) and [delta] (%s) flags", vhd, delta)
	switch {
	case vhd == "" && delta == "":
		return errors.New("must specify either the [vhd] or [delta] flag")
	case vhd != "" && delta != "":
		return errors.New("both [vhd] and [delta] flags provided - only one may be defined")
	}
	// check for extra flags
	Debugf("validating that no extra flags or arguments were provided")
	if n := len(flag.Args()); n != 0 {
		return fmt.Errorf("extra arguments: %s\n", strings.Join(flag.Args(), ", "))
	}

	Debugf("validating that the [vhd] and [delta] exist and are regular files")
	if err := validFile(vhd); err != nil {
		return fmt.Errorf("invalid [vhd]: %s", err)
	}
	if err := validFile(delta); err != nil {
		return fmt.Errorf("invalid [delta]: %s", err)
	}
	return nil
}

// Validates that version s if of
func ValidateVersion(version string) error {
	Debugf("validating version string: %s", version)
	s := strings.TrimSpace(version)
	if s == "" {
		return errors.New("missing required argument 'version'")
	}
	if !regexp.MustCompile(`^\d{1,}.\d{1,}$`).MatchString(s) {
		Debugf("expected version string to match regex: '%s'", `^\d*.*\d$`)
		return fmt.Errorf("invalid version (%s) expected format [NUMBER].[NUMBER]", s)
	}
	return nil
}

func ValidateOutputDir(dirname string) error {
	Debugf("validating output directory: %s", dirname)
	if dirname == "" {
		return nil
	}
	fi, err := os.Stat(dirname)
	if err != nil {
		return fmt.Errorf("error opening output directory (%s): %s\n", dirname, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("output argument (%s): is not a directory\n", dirname)
	}
	return nil
}

func ValidateStemcellFilename(dirname, version string) error {
	name := filepath.Join(dirname, StemcellFilename(version))
	Debugf("validating that stemcell filename (%s) does not exist", name)
	if _, err := os.Stat(name); !os.IsNotExist(err) {
		return fmt.Errorf("file (%s) already exists - refusing to overwrite", name)
	}
	return nil
}

// Validate that names consitute and ovf file
func ValidateOVFNames(names []string) error {
	Debugf("validating ovf package files: %s", strings.Join(names, ", "))

	// file extensions - for validation
	exts := make(map[string]int)
	for _, s := range names {
		exts[filepath.Ext(s)]++
	}

	// list files by ext - for error messages
	byExt := func(ext string) string {
		var a []string
		for _, s := range names {
			if filepath.Ext(s) == ext {
				a = append(a, s)
			}
		}
		return strings.Join(a, ", ")
	}

	// minimal check for required files
	// source: http://www.dmtf.org/sites/default/files/standards/documents/DSP0243_2.1.1.pdf
	//
	if n := exts[".ovf"]; n != 1 {
		if n == 0 {
			return errors.New("missing .ovf file (one is required)")
		}
		if n > 1 {
			return fmt.Errorf("multiple .ovf files (expected one): %s", byExt(".ovf"))
		}
	}
	if n := exts[".mf"]; n > 1 {
		return fmt.Errorf("multiple .mf files (expected one or zero): %s", byExt(".mf"))
	}
	if n := exts[".cert"]; n > 1 {
		return fmt.Errorf("multiple .cert files (expected one or zero): %s", byExt(".cert"))
	}

	return nil
}

func ValidateOVAFile(name string) error {
	Debugf("validating ova file: %s", name)
	f, err := os.Open(name)
	if err != nil {
		return fmt.Errorf("opening ova file (%s): %s", name, err)
	}
	defer f.Close()

	// record file names - this will be used to validate the ova
	var names []string

	// TODO: make sure the ova does not contain directories
	tr := tar.NewReader(f)
	for err == nil {
		var h *tar.Header
		h, err = tr.Next()
		if h != nil {
			names = append(names, h.Name)
			Debugf("    %s", h.Name)
		}
	}
	if err != io.EOF {
		return fmt.Errorf("invalid ova file (%s): %s", name, err)
	}
	if err := ValidateOVFNames(names); err != nil {
		return fmt.Errorf("ova (%s): %s", name, err)
	}
	return nil
}

func ExtractOVA(ova, dirname string) error {
	Debugf("extracting ova file (%s) to directory: %s", ova, dirname)

	tf, err := os.Open(ova)
	if err != nil {
		return err
	}
	defer tf.Close()

	tr := tar.NewReader(tf)

	limit := 100
	for ; limit >= 0; limit-- {
		h, err := tr.Next()
		if err != nil {
			if err != io.EOF {
				return fmt.Errorf("tar: reading from archive (%s): %s", ova, err)
			}
			break
		}

		// expect a flat archive
		name := h.Name
		if filepath.Base(name) != name {
			return fmt.Errorf("tar: archive contains subdirectory: %s", name)
		}

		// only allow regular files
		mode := h.FileInfo().Mode()
		if !mode.IsRegular() {
			return fmt.Errorf("tar: unexpected file mode (%s): %s", name, mode)
		}

		path := filepath.Join(dirname, name)
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return fmt.Errorf("tar: opening file (%s): %s", path, err)
		}
		defer f.Close()

		if _, err := io.Copy(f, tr); err != nil {
			return fmt.Errorf("tar: writing file (%s): %s", path, err)
		}
	}
	if limit <= 0 {
		return errors.New("tar: too many files in archive")
	}
	return nil
}

func StemcellFilename(version string) string {
	return fmt.Sprintf("bosh-stemcell-%s-vsphere-esxi-windows2012R2-go_agent.tgz", version)
}

var ErrInterupt = errors.New("interupt")

type CancelWriteCloser struct {
	wc   io.WriteCloser
	stop chan struct{}
}

func (w *CancelWriteCloser) Write(p []byte) (int, error) {
	select {
	case <-w.stop:
		return 0, ErrInterupt
	default:
		return w.wc.Write(p)
	}
}

func (w *CancelWriteCloser) Close() error {
	return w.wc.Close()
}

type CancelWriter struct {
	w    io.Writer
	stop chan struct{}
}

func (w *CancelWriter) Write(p []byte) (int, error) {
	select {
	case <-w.stop:
		return 0, ErrInterupt
	default:
		return w.w.Write(p)
	}
}

type CancelReader struct {
	r    io.Reader
	stop chan struct{}
}

func (r *CancelReader) Read(p []byte) (int, error) {
	select {
	case <-r.stop:
		return 0, ErrInterupt
	default:
		return r.r.Read(p)
	}
}

type Config struct {
	Image    string
	Stemcell string
	Manifest string
	Sha1sum  string
	tmpdir   string
	stop     chan struct{}
}

// returns a io.Writer that returns an error when Config c is stopped
func (c *Config) Writer(w io.Writer) *CancelWriter {
	return &CancelWriter{w: w, stop: c.stop}
}

// returns a io.Reader that returns an error when Config c is stopped
func (c *Config) Reader(r io.Reader) *CancelReader {
	return &CancelReader{r: r, stop: c.stop}
}

func (c *Config) Stop() {
	Debugf("stopping config")
	defer c.Cleanup() // make sure this runs!
	close(c.stop)
}

func (c *Config) Cleanup() {
	if c.tmpdir != "" {
		Debugf("deleting temp directory: %s", c.tmpdir)
		os.RemoveAll(c.tmpdir)
	}
}

func (c *Config) AddTarFile(tr *tar.Writer, name string) error {
	Debugf("adding file (%s) to tar archive", name)
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	hdr, err := tar.FileInfoHeader(fi, "")
	if err != nil {
		return err
	}
	if err := tr.WriteHeader(hdr); err != nil {
		return err
	}
	if _, err := io.Copy(tr, c.Reader(f)); err != nil {
		return err
	}
	return nil
}

func (c *Config) TempDir() (string, error) {
	if c.tmpdir != "" {
		if _, err := os.Stat(c.tmpdir); err != nil {
			Debugf("unable to stat temp dir (%s) was it deleted?", c.tmpdir)
			return "", fmt.Errorf("opening temp directory: %s", c.tmpdir)
		}
		return c.tmpdir, nil
	}
	name, err := ioutil.TempDir("", "ova2stemcell-")
	if err != nil {
		return "", fmt.Errorf("creating temp directory: %s", err)
	}
	c.tmpdir = name
	Debugf("created temp directory: %s", name)
	return c.tmpdir, nil
}

func (c *Config) CreateStemcell() error {
	Debugf("creating stemcell")

	// programming errors - panic!
	if c.Manifest == "" {
		panic("CreateStemcell: empty manifest")
	}
	if c.Image == "" {
		panic("CreateStemcell: empty image")
	}

	tmpdir, err := c.TempDir()
	if err != nil {
		return err
	}

	c.Stemcell = filepath.Join(tmpdir, StemcellFilename(Version))
	stemcell, err := os.OpenFile(c.Stemcell, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer stemcell.Close()
	Debugf("created temp stemcell: %s", c.Stemcell)

	errorf := func(format string, a ...interface{}) error {
		stemcell.Close()
		os.Remove(c.Stemcell)
		return fmt.Errorf(format, a...)
	}

	t := time.Now()
	w := gzip.NewWriter(c.Writer(stemcell))
	tr := tar.NewWriter(w)

	Debugf("adding image file to stemcell tarball: %s", c.Image)
	if err := c.AddTarFile(tr, c.Image); err != nil {
		return errorf("creating stemcell: %s", err)
	}

	Debugf("adding manifest file to stemcell tarball: %s", c.Manifest)
	if err := c.AddTarFile(tr, c.Manifest); err != nil {
		return errorf("creating stemcell: %s", err)
	}

	if err := tr.Close(); err != nil {
		return errorf("creating stemcell: %s", err)
	}

	if err := w.Close(); err != nil {
		return errorf("creating stemcell: %s", err)
	}

	Debugf("created stemcell in: %s", time.Since(t))

	return nil
}

// WARN WARN WARN
//
// ADD FILES TO TAR IN PROPER ORDER!!!!
//
func (c *Config) CreateImageFromOVF(dirname string) error {
	Debugf("creating ova file from directory: %s", dirname)

	fis, err := ioutil.ReadDir(dirname)
	if err != nil {
		return fmt.Errorf("ovf directory (%s): %s", dirname, err)
	}

	tmpdir, err := c.TempDir()
	if err != nil {
		return err
	}

	c.Image = filepath.Join(tmpdir, "image")
	image, err := os.OpenFile(c.Image, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("creating image file (%s): %s", c.Image, err)
	}
	defer image.Close()

	errorf := func(format string, a ...interface{}) error {
		image.Close()
		os.Remove(c.Image)
		return fmt.Errorf(format, a...)
	}

	Debugf("created temp image file: %s", c.Image)

	// Wrap file f with c.Writer so that writes can be cancelled
	w := gzip.NewWriter(c.Writer(image))
	t := time.Now()
	h := sha1.New()
	tr := tar.NewWriter(io.MultiWriter(h, w))

	for _, fi := range fis {
		path := filepath.Join(dirname, fi.Name())
		if err := c.AddTarFile(tr, path); err != nil {
			return errorf("adding file (%s) to image (%s) archive: %s",
				dirname, path, err)
		}
	}

	if err := tr.Close(); err != nil {
		return errorf("creating ova from directory (%s): %s", dirname, err)
	}
	if err := w.Close(); err != nil {
		return errorf("creating ova from directory (%s): %s", dirname, err)
	}
	Debugf("created image file in: %s", time.Since(t))

	c.Sha1sum = fmt.Sprintf("%x", h.Sum(nil))
	Debugf("sha1 checksum of image file is: %s", c.Sha1sum)

	return nil
}

func (c *Config) CreateImageFromOVA(name string) error {
	Debugf("creating image fime from ova: %s", name)

	ova, err := os.Open(name)
	if err != nil {
		return fmt.Errorf("opening ova file (%s): %s", name, err)
	}
	defer ova.Close()

	tmpdir, err := c.TempDir()
	if err != nil {
		return err
	}

	c.Image = filepath.Join(tmpdir, "image")
	image, err := os.OpenFile(c.Image, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("creating image file (%s): %s", c.Image, err)
	}
	defer image.Close()
	Debugf("created temp image file: %s", c.Image)

	Debugf("compressing ova (%s) with gzip to image file: %s", name, c.Image)

	h := sha1.New()
	t := time.Now()
	w := gzip.NewWriter(c.Writer(io.MultiWriter(h, image)))
	if _, err := io.Copy(w, ova); err != nil {
		os.Remove(c.Image)
		return fmt.Errorf("writing image (%s): %s", c.Image, err)
	}
	if err := w.Close(); err != nil {
		os.Remove(c.Image)
		return fmt.Errorf("writing image (%s): %s", c.Image, err)
	}
	Debugf("created image file in: %s", time.Since(t))

	c.Sha1sum = fmt.Sprintf("%x", h.Sum(nil))
	Debugf("sha1 checksum of image file is: %s", c.Sha1sum)

	return nil
}

func (c *Config) WriteManifest() error {
	const format = `---
name: bosh-vsphere-esxi-windows-2012R2-go_agent
version: %s
sha1: %s
operating_system: windows2012R2
cloud_properties:
  infrastructure: vsphere
  hypervisor: esxi
`

	// programming error - this should never happen...
	if c.Manifest != "" {
		panic("already created manifest: " + c.Manifest)
	}

	tmpdir, err := c.TempDir()
	if err != nil {
		return err
	}

	c.Manifest = filepath.Join(tmpdir, "stemcell.MF")
	f, err := os.OpenFile(c.Manifest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("creating stemcell.MF (%s): %s", c.Manifest, err)
	}
	defer f.Close()
	Debugf("created temp stemcell.MF file: %s", c.Manifest)

	if _, err := fmt.Fprintf(f, format, Version, c.Sha1sum); err != nil {
		os.Remove(c.Manifest)
		return fmt.Errorf("writing stemcell.MF (%s): %s", c.Manifest, err)
	}
	Debugf("wrote stemcell.MF with sha1: %s and version: %s", c.Sha1sum, Version)

	return nil
}

func ParseFlags() error {
	flag.Parse()
	Version = strings.TrimSpace(Version)
	VHDFile = strings.TrimSpace(VHDFile)
	DeltaFile = strings.TrimSpace(DeltaFile)
	OutputDir = strings.TrimSpace(OutputDir)

	if EnableDebug {
		Debugf = log.New(os.Stderr, "debug: ", 0).Printf
		Debugf("enabled")
	}

	if err := ValidateInputFlags(VHDFile, DeltaFile); err != nil {
		return err
	}

	if OutputDir == "" || OutputDir == "." {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting working directory: %s", err)
		}
		Debugf("set output dir (%s) to working directory: %s", OutputDir, wd)
		OutputDir = wd
	}

	return nil
}

func ConvertVMX2OVA(vmx, ova string) error {
	fmt.Println("converting vmx to ova")
	cmd := exec.Command("ovftool", vmx, ova)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("converting vmx to ova: %s\noutput:\n%s\n",
			stderr.String())
	}
	return nil
}

func realMain(vhd, delta, version string, c *Config) error {
	start := time.Now()

	// PATCH HERE
	tmpdir, err := c.TempDir()
	if err != nil {
		return err
	}
	patchedVMDK := filepath.Join(tmpdir, "image.vmdk")
	if err := rdiff.Patch(vhd, delta, patchedVMDK, true); err != nil {
		return err
	}

	vmxPath := filepath.Join(tmpdir, "image.vmx")
	vmxFile, err := os.OpenFile(vmxPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	if err := VMXTemplate(patchedVMDK, vmxFile); err != nil {
		return err
	}
	vmxFile.Close()

	ovaPath := filepath.Join(tmpdir, "image.ova")
	if err := ConvertVMX2OVA(vmxPath, ovaPath); err != nil {
		return err
	}

	ovfDir := filepath.Join(tmpdir, "ovf")
	os.Mkdir(ovfDir, 0755)
	_ = OvfDir

	if err := c.WriteManifest(); err != nil {
		return err
	}
	if err := c.CreateStemcell(); err != nil {
		return err
	}

	stemcellPath := filepath.Join(OutputDir, filepath.Base(c.Stemcell))
	Debugf("moving stemcell (%s) to: %s", c.Stemcell, stemcellPath)

	if err := os.Rename(c.Stemcell, stemcellPath); err != nil {
		return err
	}
	Debugf("created stemcell (%s) in: %s", stemcellPath, time.Since(start))
	fmt.Println("created stemell:", stemcellPath)

	return nil
}

func main() {
	tarfile := os.Args[1]
	_ = tarfile
}

func xmain() {
	if err := ParseFlags(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		Usage()
	}

	if _, err := exec.LookPath("ovftool"); err != nil {
		fmt.Fprintf(os.Stderr, "could not locate 'ovftool' on PATH: %s", err)
		Usage()
	}

	if err := ValidateVersion(Version); err != nil {
		fmt.Fprintln(os.Stderr, err)
		Usage()
	}
	if err := ValidateOutputDir(OutputDir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		Usage()
	}
	if err := ValidateStemcellFilename(OutputDir, Version); err != nil {
		fmt.Fprintln(os.Stderr, err)
		Usage()
	}

	c := Config{stop: make(chan struct{})}

	// cleanup if interupted
	go func() {
		// WARN Make sure we don't exit for things like
		// TERM signals
		ch := make(chan os.Signal, 64)
		signal.Notify(ch)
		stopping := false
		for sig := range ch {
			if stopping {
				fmt.Fprintf(os.Stderr, "recieved second (%s) signale - exiting now\n", sig)
				os.Exit(1)
			}
			stopping = true
			fmt.Fprintf(os.Stderr, "recieved (%s) signal cleaning up\n", sig)
			c.Stop()
		}
	}()

	if err := realMain(VHDFile, DeltaFile, Version, &c); err != nil {
		c.Cleanup()
		// FOO
	}

}
