package schema

import (
	"errors"
	"slices"
	"testing"
	"testing/fstest"
)

// minimalPack is a valid project pack.yaml, enough for a pack to load.
const minimalPack = "name: test\nversion: 0.1.0\ncore_version: 0.1.0\n"

// minimalCorePack is a valid core pack.yaml: no core_version, since a core
// pack cannot pin itself.
const minimalCorePack = "name: core\nversion: 0.1.0\n"

// fsWith builds a project pack filesystem, supplying pack.yaml when the caller
// did not. A caller testing a missing pack.yaml builds the fstest.MapFS itself.
func fsWith(files map[string]string) fstest.MapFS {
	m := fstest.MapFS{}
	if _, ok := files["pack.yaml"]; !ok {
		m["pack.yaml"] = &fstest.MapFile{Data: []byte(minimalPack)}
	}
	for name, body := range files {
		m[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return m
}

// corePackFS builds a synthetic core pack, so version-skew cases can vary the
// core version without touching the embedded one.
func corePackFS(t *testing.T, packYAML string, files map[string]string) *Pack {
	t.Helper()
	m := fstest.MapFS{"pack.yaml": &fstest.MapFile{Data: []byte(packYAML)}}
	for name, body := range files {
		m[name] = &fstest.MapFile{Data: []byte(body)}
	}
	p, err := loadFS(m, SourceCore)
	if err != nil {
		t.Fatalf("building synthetic core pack: %v", err)
	}
	return p
}

// codesOf extracts the sorted, deduplicated error codes from err. Tests assert
// on codes rather than message text so wording stays free to improve.
func codesOf(t *testing.T, err error) []Code {
	t.Helper()
	if err == nil {
		return nil
	}
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error is not schema.Errors: %T: %v", err, err)
	}
	var out []Code
	for _, e := range errs {
		if !slices.Contains(out, e.Code) {
			out = append(out, e.Code)
		}
	}
	slices.Sort(out)
	return out
}

// wantCodes asserts that err carries exactly the given codes.
func wantCodes(t *testing.T, err error, want ...Code) {
	t.Helper()
	slices.Sort(want)
	got := codesOf(t, err)
	if !slices.Equal(got, want) {
		t.Errorf("error codes = %v, want %v\nfull error: %v", got, want, err)
	}
}

// asErrors reports whether err is a schema.Errors, unwrapping if needed.
func asErrors(err error, target *Errors) bool {
	return errors.As(err, target)
}

// mustLoadFixture loads the on-disk project fixture merged over the real core.
func mustLoadFixture(t *testing.T) *Pack {
	t.Helper()
	p, err := LoadProject("testdata/project-ok")
	if err != nil {
		t.Fatalf("loading fixture project: %v", err)
	}
	return p
}
