// Package mcp serves the lore graph to agents over the Model Context Protocol.
//
// Every read goes through internal/resolve under a Context built from the
// session role, which is fixed at launch: no tool argument can widen it. The
// only write is a proposal under proposals/<id>/; nothing here writes world/
// or schema/.
package mcp

import (
	"context"
	"errors"
	"io"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Role is the session role. It sets the visibility ceiling and whether a read
// without a knower is allowed.
type Role string

// RoleAuthor is the one role for now: ceiling internal, omniscient reads
// allowed.
const RoleAuthor Role = "author"

// Config is what a server is launched with.
type Config struct {
	Repo string // the project directory, holding schema/ and world/
	Role Role
}

// implementation names the server to clients.
var implementation = &sdk.Implementation{Name: "lorekeep"}

// ErrNotImplemented is returned by stubs until their task lands.
var ErrNotImplemented = errors.New("not implemented")

// Serve speaks MCP over in and out until in closes or ctx is done. out
// carries JSON-RPC only; nothing else may write to it.
func Serve(ctx context.Context, cfg Config, in io.Reader, out io.Writer) error {
	s, err := New(cfg)
	if err != nil {
		return err
	}
	rc, ok := in.(io.ReadCloser)
	if !ok {
		rc = io.NopCloser(in)
	}
	return s.mcp.Run(ctx, &sdk.IOTransport{Reader: rc, Writer: nopWriteCloser{out}})
}

// nopWriteCloser leaves out open when the session ends: the caller owns it.
type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
