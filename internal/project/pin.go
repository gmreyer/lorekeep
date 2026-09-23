// Package project holds what lorekeep knows about a world directory as a
// whole, rather than about its schema pack or its authored content.
package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// PinFile names the file that pins the lorekeep version a world is built with.
//
// Its format is frozen: one line holding a release version such as v0.3.0.
// Every lorekeep from the first that reads it will keep reading it, including
// old binaries dispatching to new ones, so nothing may ever be added to it.
const PinFile = "lorekeep-version"

// releasePattern is a release tag: vMAJOR.MINOR.PATCH, nothing more. A
// pre-release or a pseudo-version is not something a world can pin, because
// only release tags have a lorekeep.exe to download.
var releasePattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// IsRelease reports whether v is a release version a world can pin.
func IsRelease(v string) bool {
	return releasePattern.MatchString(v)
}

// ErrNoPin means the directory has no pin file.
var ErrNoPin = errors.New("no " + PinFile + " file")

// ReadPin returns the version pinned in dir.
//
// A trailing newline, CRLF or LF, is accepted because editors add one; any
// other content is an error rather than something to guess around.
func ReadPin(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, PinFile))
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNoPin
	}
	if err != nil {
		return "", err
	}
	return ParsePin(string(data))
}

// ParsePin parses the contents of a pin file.
func ParsePin(s string) (string, error) {
	v := strings.TrimSuffix(strings.TrimSuffix(s, "\n"), "\r")
	if !IsRelease(v) {
		return "", fmt.Errorf("%s: want one line like v1.2.3, got %q", PinFile, s)
	}
	return v, nil
}

// WritePin pins dir to version v.
func WritePin(dir, v string) error {
	if !IsRelease(v) {
		return fmt.Errorf("cannot pin %q: not a release version like v1.2.3", v)
	}
	return os.WriteFile(filepath.Join(dir, PinFile), []byte(v+"\n"), 0o644)
}
