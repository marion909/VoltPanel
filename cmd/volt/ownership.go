package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// alignDBOwnership gibt die Datenbank nach einem Aufruf als root wieder dem
// Benutzer zurück, unter dem die Dienste laufen.
//
// Der Grund ist SQLite im WAL-Modus: neben volt.db entstehen volt.db-wal und
// volt.db-shm, und sie gehören dem Prozess, der sie anlegt. Wer direkt nach der
// Installation als root `volt site add` tippt — der naheliegendste Fall
// überhaupt —, hinterlässt so Dateien, die volt-web anschließend nicht mehr
// beschreiben kann. Der Dienst scheitert dann mit "attempt to write a readonly
// database", obwohl die Rechte an volt.db selbst stimmen.
//
// Als Ziel dient der Eigentümer des Datenverzeichnisses, nicht der von
// volt.db: so greift die Korrektur auch, wenn die Datenbank in diesem Aufruf
// erst entsteht.
func alignDBOwnership(dbPath string) error {
	if os.Geteuid() != 0 {
		return nil
	}

	info, err := os.Stat(filepath.Dir(dbPath))
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid == 0 {
		return nil
	}

	for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		fi, err := os.Stat(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		cur, ok := fi.Sys().(*syscall.Stat_t)
		if !ok || (cur.Uid == st.Uid && cur.Gid == st.Gid) {
			continue
		}
		if err := os.Chown(p, int(st.Uid), int(st.Gid)); err != nil {
			return err
		}
	}
	return nil
}
