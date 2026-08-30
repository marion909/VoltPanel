//go:build unix

package agent

import (
	"os"
	"os/user"
	"strconv"
	"syscall"
)

// ownerNames übersetzt UID/GID einer Datei in Namen. Schlägt die Auflösung fehl
// (User existiert nicht mehr), bleibt die numerische ID stehen.
func ownerNames(info os.FileInfo) (string, string) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", ""
	}
	uid, gid := strconv.FormatUint(uint64(st.Uid), 10), strconv.FormatUint(uint64(st.Gid), 10)

	owner, group := uid, gid
	if u, err := user.LookupId(uid); err == nil {
		owner = u.Username
	}
	if g, err := user.LookupGroupId(gid); err == nil {
		group = g.Name
	}
	return owner, group
}
