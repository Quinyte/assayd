package controller

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// A named ConditionType does NOT make a typo a compile error, and believing it
// did was the mistake this test exists to prevent repeating.
//
// Go converts an untyped constant implicitly, so `c.set("Degradedd", …)` builds
// cleanly against a `ConditionType` parameter — verified. The type helps when a
// `string` VARIABLE is passed and nowhere else. What actually closes the
// vocabulary is this: every condition type at a call site must be one of the
// declared constants, and a string literal there is a finding.
//
// The alternative was CEL on the CRD, which was tried and withdrawn (A52): one
// unrecognised type rejects the entire status write, so an operator asserting a
// condition its installed CRD does not list loses phase, activeRevisionDigest
// and Ready for that Agent and error-loops. That is a far worse failure than
// the typo it catches.
func TestNoConditionTypeIsALiteral(t *testing.T) {
	var offenders []string
	err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Ext(path) != ".go" {
			return err
		}
		if len(path) > 8 && path[len(path)-8:] == "_test.go" {
			return nil // a test may assert on a literal deliberately
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range regexp.MustCompile(`\.set\(\s*"([^"]*)"`).FindAllStringSubmatch(string(b), -1) {
			offenders = append(offenders, path+`: .set("`+m[1]+`"`)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the package: %v", err)
	}
	for _, o := range offenders {
		t.Errorf("a condition type is a string literal: %s\n"+
			"Use the declared constant. A literal converts implicitly to ConditionType, so it "+
			"compiles, runs, and leaves every consumer watching the correct spelling silent "+
			"through the degradation it was meant to announce.", o)
	}
}
