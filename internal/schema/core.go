package schema

import (
	"embed"
	"io/fs"
	"sync"
)

// The core pack ships inside the module rather than being copied into a world
// repo, so a fix reaches every existing project through a version bump.
//
//go:embed core/*.yaml
var coreFiles embed.FS

var (
	coreOnce sync.Once
	corePack *Pack
	coreErr  error
)

// Core returns the embedded core schema pack.
//
// The returned pack is a process-wide singleton, so callers must not modify
// it; Merge copies rather than writing through to it.
func Core() (*Pack, error) {
	coreOnce.Do(func() {
		sub, err := fs.Sub(coreFiles, "core")
		if err != nil {
			coreErr = err
			return
		}
		corePack, coreErr = loadFS(sub, SourceCore)
	})
	return corePack, coreErr
}

// CoreVersion is the version of the embedded core pack. It is read from the
// pack itself rather than held as a Go constant or stamped from a git tag at
// build time: a tag-derived version is empty under go test and needs build
// flags to be right, and a constant would be a second place for the same fact.
func CoreVersion() (string, error) {
	c, err := Core()
	if err != nil {
		return "", err
	}
	return c.Version, nil
}
