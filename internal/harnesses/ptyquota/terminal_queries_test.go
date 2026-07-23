package ptyquota

import (
	"bytes"
	"testing"
)

// grokStartupBurst is the real startup byte sequence grok 0.2.106 emits
// before its first frame; it blocks until the DSR cursor-position query is
// answered.
var grokStartupBurst = []byte("\x1b]0;grok\x07\x1b[?1000h\x1b[?1002h\x1b[?1003h\x1b[?1015h\x1b[?1006h\x1b[?1004h\x1b[?2004h\x1b[?25l\x1b]12;rgb:c6/c6/c6\x07\x1b[6n\x1b[1;1H\x1b[>0q\x1b[?2026h")

func TestScanTerminalQueriesGrokStartup(t *testing.T) {
	reply := scanTerminalQueries(grokStartupBurst, 0, 3, 7)
	if !bytes.Contains(reply, []byte("\x1b[3;7R")) {
		t.Errorf("reply %q missing DSR cursor position report", reply)
	}
	if !bytes.Contains(reply, []byte("\x1bP>|fizeau-ptyquota\x1b\\")) {
		t.Errorf("reply %q missing XTVERSION response", reply)
	}
}

func TestScanTerminalQueriesNoQueries(t *testing.T) {
	if reply := scanTerminalQueries([]byte("plain text output\x1b[1;1H"), 0, 1, 1); len(reply) != 0 {
		t.Errorf("unexpected reply %q for query-free output", reply)
	}
}

func TestScanTerminalQueriesTailNotReanswered(t *testing.T) {
	window := []byte("prefix\x1b[6nsuffix")
	// First scan answers the query.
	if reply := scanTerminalQueries(window, 0, 1, 1); len(reply) == 0 {
		t.Fatal("expected DSR reply on first scan")
	}
	// A re-scan where the query lies entirely within the retained tail
	// (before start) must not answer again.
	if reply := scanTerminalQueries(window, len(window), 1, 1); len(reply) != 0 {
		t.Errorf("query in retained tail answered twice: %q", reply)
	}
}

func TestScanTerminalQueriesSplitAcrossChunks(t *testing.T) {
	full := []byte("\x1b[6n")
	head, tail := full[:2], full[2:]
	// Chunk 1: no complete query yet.
	if reply := scanTerminalQueries(head, 0, 1, 1); len(reply) != 0 {
		t.Fatalf("incomplete query answered: %q", reply)
	}
	// Chunk 2: window = retained tail + new bytes; query completes inside
	// the new region.
	window := append(append([]byte(nil), head...), tail...)
	if reply := scanTerminalQueries(window, len(head), 1, 1); !bytes.Contains(reply, []byte("\x1b[1;1R")) {
		t.Fatalf("split query not answered: %q", reply)
	}
}

func TestScanTerminalQueriesDeviceAttributes(t *testing.T) {
	reply := scanTerminalQueries([]byte("\x1b[c\x1b[>c"), 0, 1, 1)
	if !bytes.Contains(reply, []byte("\x1b[?1;0c")) {
		t.Errorf("reply %q missing DA1 response", reply)
	}
	if !bytes.Contains(reply, []byte("\x1b[>0;0;0c")) {
		t.Errorf("reply %q missing DA2 response", reply)
	}
}
