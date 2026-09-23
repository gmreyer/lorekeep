// Package project holds what lorekeep knows about a world directory as a
// whole, rather than about its schema pack or its authored content.
package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// PinFile names the file that pins the lorekeep version a world is built with.
//
// Its format is frozen: one line holding a release version such as v0.3.0.
// Every lorekeep from the first that reads it will keep reading it, including
// old binaries dispatching to new ones, so nothing may ever be added to it.
const PinFile = "lorekeep-version"

// FirstPinVersion is the first lorekeep that reads PinFile. A project cannot
// pin anything older: an older binary would ignore the pin it was run for.
const FirstPinVersion = "v0.3.0"

// releasePattern is a release tag: vMAJOR.MINOR.PATCH, nothing more. A
// pre-release or a pseudo-version is not something a world can pin, because
// only release tags have a lorekeep.exe to download.
var releasePattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// IsRelease reports whether v is a release version a world can pin.
func IsRelease(v string) bool {
	return releasePattern.MatchString(v)
}

// Compare orders two release versions: -1 if a is older than b, 0 if they are
// equal, +1 if a is newer. Both must satisfy IsRelease.
func Compare(a, b string) int {
	pa, pb := releasePattern.FindStringSubmatch(a), releasePattern.FindStringSubmatch(b)
	if pa == nil || pb == nil {
		panic(fmt.Sprintf("project.Compare(%q, %q): not release versions", a, b))
	}
	for i := 1; i <= 3; i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
	}
	return 0
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
