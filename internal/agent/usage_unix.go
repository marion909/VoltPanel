//go:build unix

package agent

import (
	"os"
	"syscall"
)

// diskBlocks liefert den tatsächlich belegten Platz und die Inode-Nummer.
// Ein Block ist per POSIX-Definition 512 Bytes, unabhängig von der
// Blockgröße des Dateisystems.
func diskBlocks(info os.FileInfo) (bytes int64, inode uint64, ok bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return st.Blocks * 512, st.Ino, true
}
