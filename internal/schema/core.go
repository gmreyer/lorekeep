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

// The core pack's own vocabulary, named here because a few core rules are
// about specific core concepts rather than about the shape of the graph.
//
// These are the only vocabulary names in Go anywhere, and the line is worth
// stating: a RELATION name must never appear in code, because relations are
// what a project renames and a hardcoded one would break reuse on day one.
// Roles and the core entity types are different — both are closed, core-owned
// sets that a project extends but cannot rename, and roles exist precisely so
// that rules and views can bind to something stable. TestCoreDeclaresNamedVocabulary
// fails loudly if the core pack ever stops declaring one of these.
const (
	TypeCharacter = "character"
	TypeEvent     = "event"
	TypeDecision  = "decision"

	RoleParticipation Role = "participation"
	RoleContainment   Role = "containment"
	RoleMembership    Role = "membership"
	RoleOrdering      Role = "ordering"
)

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
