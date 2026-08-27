// Package logsafe strips what would let a request forge a log entry.
package logsafe

import "strings"

// A value that reaches a log has to arrive as one line. Newlines let a request
// path write an entry of its own choosing underneath the real one, which is how
// a log stops being evidence.
var stripper = strings.NewReplacer("\n", "", "\r", "", "\t", " ")

// Value returns s with the characters that would break a log line removed.
func Value(s string) string { return stripper.Replace(s) }
