// Package jev is a herrscher compaction stage that asks Jev, the System One
// model of TypeSafe, which tool calls of a session still matter, and prunes the
// others from the history its backend keeps. A blank import registers it under
// the kind "jev", and a session runs it ahead of its backend's own compaction
// with the stack "jev,backend".
//
// It is a Go port of fast-jev-compaction. It needs a backend that edits its
// history in place, one whose manifest declares Capabilities.HistoryEditor, and
// a TypeSafe API key in TYPESAFE_API_KEY.
package jev
