// Package version hält die Build-Informationen, die GoReleaser per -ldflags setzt.
package version

import "fmt"

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
	Channel = "stable" // stable | beta
)

// Full liefert die Zeile, die `volt --version` und /api/v1/system/version ausgeben.
func Full() string {
	return fmt.Sprintf("volt %s (%s, %s, channel=%s)", Version, Commit, Date, Channel)
}

// SchemaVersion ist die höchste Migration, die dieses Binary kennt. Startet das
// Binary gegen eine neuere DB, bricht es ab statt das Schema zu beschädigen.
const SchemaVersion = 3
