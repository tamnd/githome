// Package gittransport serves git clone and fetch over the Git Smart HTTP
// protocol. Following the locked design decision, it shells out to the system
// git binary (git upload-pack), which gives correct v0 and v2 negotiation,
// delta resolution, thin packs, and side-band framing for free. M2 implements
// the read side (upload-pack); the write side (receive-pack) and SSH land with
// later milestones.
//
// The package sits at the bottom of the dependency graph next to git: it calls
// the domain repo service for the read-permission check and the git store to
// resolve a repository to its on-disk path. It never imports store or presenter
// and never builds a filesystem path from the request's owner or repo strings.
package gittransport

import (
	"fmt"
	"io"
)

// flushPkt is the pkt-line that marks a section boundary: the four bytes "0000".
const flushPkt = "0000"

// maxPktLen is the largest a pkt-line may be, including its 4-byte length prefix
// (protocol-common). The payload may therefore be at most maxPktLen-4 bytes.
const maxPktLen = 65520

// writePktString frames s as a single data pkt-line: a four-hex length prefix
// (the payload length plus the four prefix bytes) followed by the payload. Any
// trailing newline must already be part of s. It is the one place that emits the
// pkt-line header; the git subprocess produces the rest of the stream itself, so
// the only line Githome writes is the service-advertisement preamble.
func writePktString(w io.Writer, s string) error {
	n := len(s) + 4
	if n > maxPktLen {
		return fmt.Errorf("gittransport: pkt-line payload exceeds %d bytes", maxPktLen-4)
	}
	if _, err := fmt.Fprintf(w, "%04x", n); err != nil {
		return err
	}
	_, err := io.WriteString(w, s)
	return err
}

// writeFlush writes the flush-pkt that terminates the preamble section.
func writeFlush(w io.Writer) error {
	_, err := io.WriteString(w, flushPkt)
	return err
}
