package rdiff_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"testing"
)

func TestRdiff(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Rdiff Suite")
}
