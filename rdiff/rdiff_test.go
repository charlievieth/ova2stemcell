package rdiff_test

import (
	"io/ioutil"
	"os"
	"path"
	"path/filepath"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/pivotal-cf-experimental/pcf-make-stemcell/rdiff"
)

var _ = Describe("Rdiff", func() {
	Describe("Patch", func() {
		It("takes a file and patch file, returns new file with patch applied", func() {
			rootDir := path.Join(os.Getenv("GOPATH"), "src/github.com/pivotal-cf-experimental/pcf-make-stemcell")
			prePatch := path.Join(rootDir, "fixtures", "pre-patch")
			postPatch := path.Join(rootDir, "fixtures", "post-patch")
			patchFile := path.Join(rootDir, "fixtures", "patchfile")

			dirname, err := ioutil.TempDir("", "rdiff-test-")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(dirname)
			newFile := filepath.Join(dirname, "some-patched-file")

			Expect(rdiff.Patch(prePatch, patchFile, newFile, true)).ToNot(HaveOccurred())

			patchedFileBytes, _ := ioutil.ReadFile(newFile)
			patchedFileText := string(patchedFileBytes)
			postPatchBytes, _ := ioutil.ReadFile(postPatch)
			postPatchText := string(postPatchBytes)

			Expect(patchedFileText).To(Equal(postPatchText))
		})
	})
})
