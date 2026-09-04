package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

// maxReadBytes begrenzt, was der Agent an den Web-Prozess zurückgibt. Ohne
// Deckel würde ein `file.read` auf ein 5-GB-Log beide Prozesse mitnehmen.
const maxReadBytes = 8 << 20 // 8 MiB

func (s *Server) opFileWrite(_ context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[FileWriteParams](raw, OpFileWrite)
	if err != nil {
		return nil, err
	}
	path, err := jail(p.Path, s.roots)
	if err != nil {
		return nil, err
	}

	mode := os.FileMode(p.Mode)
	if mode == 0 {
		mode = 0o644
	}
	// Kein setuid/setgid/sticky aus dem Web-Prozess.
	mode &= 0o777

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, opErr(OpFileWrite, "verzeichnis anlegen: %v", err)
	}
	if err := writeFileAtomic(path, []byte(p.Content), mode); err != nil {
		return nil, opErr(OpFileWrite, "%v", err)
	}
	if p.Owner != "" {
		if err := checkUsername(p.Owner); err != nil {
			return nil, err
		}
		if p.Group != "" {
			if err := checkFileGroup(p.Group); err != nil {
				return nil, err
			}
		}
	}
	if err := applyOwner(path, p.Owner, p.Group, false); err != nil {
		return nil, opErr(OpFileWrite, "%v", err)
	}
	return TextResult{Text: path}, nil
}

func (s *Server) opFileRead(_ context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[FilePathParams](raw, OpFileRead)
	if err != nil {
		return nil, err
	}
	path, err := jail(p.Path, s.roots)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, opErr(OpFileRead, "%v", err)
	}
	if info.IsDir() {
		return nil, opErr(OpFileRead, "%s ist ein verzeichnis", p.Path)
	}
	if info.Size() > maxReadBytes {
		return nil, opErr(OpFileRead, "datei ist %d bytes groß, maximum sind %d", info.Size(), maxReadBytes)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		return nil, opErr(OpFileRead, "%v", err)
	}
	return TextResult{Text: string(b)}, nil
}

func (s *Server) opFileRemove(_ context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[FilePathParams](raw, OpFileRemove)
	if err != nil {
		return nil, err
	}
	path, err := jail(p.Path, s.roots)
	if err != nil {
		return nil, err
	}

	// Eine der erlaubten Wurzeln selbst zu löschen wäre ein Totalschaden.
	for _, root := range s.roots {
		if real, err := filepath.EvalSymlinks(root); err == nil && real == path {
			return nil, opErr(OpFileRemove, "%s ist ein basisverzeichnis und wird nicht gelöscht", path)
		}
	}

	if p.Recursive {
		if err := os.RemoveAll(path); err != nil {
			return nil, opErr(OpFileRemove, "%v", err)
		}
	} else if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, opErr(OpFileRemove, "%v", err)
	}
	return TextResult{Text: path}, nil
}

func (s *Server) opFileMkdir(_ context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[FileMkdirParams](raw, OpFileMkdir)
	if err != nil {
		return nil, err
	}
	path, err := jail(p.Path, s.roots)
	if err != nil {
		return nil, err
	}

	// setgid ist erlaubt, setuid und sticky nicht. Mit setgid erben neue
	// Dateien die Gruppe des Verzeichnisses — nur so bleibt ein Upload, den
	// PHP anlegt, für den Webserver lesbar, unabhängig von dessen umask.
	// setuid auf einem Verzeichnis bewirkt unter Linux ohnehin nichts und
	// hätte hier nur den Zweck, verwirrend auszusehen.
	mode := os.FileMode(p.Mode) & 0o2777
	if mode&0o777 == 0 {
		mode |= 0o750
	}
	perm := mode & 0o777
	if err := os.MkdirAll(path, perm); err != nil {
		return nil, opErr(OpFileMkdir, "%v", err)
	}
	// Erst der Eigentümer, dann die Rechte. Die Reihenfolge ist nicht
	// beliebig: chown löscht setgid wieder — auf manchen Systemen auch bei
	// Verzeichnissen. Wer zuerst chmod aufruft, verliert das Bit still.
	if p.Owner != "" {
		if err := checkUsername(p.Owner); err != nil {
			return nil, err
		}
		if p.Group != "" {
			if err := checkFileGroup(p.Group); err != nil {
				return nil, err
			}
		}
	}
	if err := applyOwner(path, p.Owner, p.Group, false); err != nil {
		return nil, opErr(OpFileMkdir, "%v", err)
	}

	// MkdirAll respektiert die umask und kennt kein setgid; beides nachziehen.
	chmod := perm
	if mode&0o2000 != 0 {
		chmod |= os.ModeSetgid
	}
	if err := os.Chmod(path, chmod); err != nil {
		return nil, opErr(OpFileMkdir, "%v", err)
	}
	return TextResult{Text: path}, nil
}

func (s *Server) opFileChown(_ context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[FileChownParams](raw, OpFileChown)
	if err != nil {
		return nil, err
	}
	path, err := jail(p.Path, s.roots)
	if err != nil {
		return nil, err
	}
	if err := checkUsername(p.Owner); err != nil {
		return nil, err
	}
	if p.Group != "" {
		if err := checkFileGroup(p.Group); err != nil {
			return nil, err
		}
	}
	if err := applyOwner(path, p.Owner, p.Group, p.Recursive); err != nil {
		return nil, opErr(OpFileChown, "%v", err)
	}
	return TextResult{Text: path}, nil
}

func (s *Server) opFileList(_ context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[FilePathParams](raw, OpFileList)
	if err != nil {
		return nil, err
	}
	path, err := jail(p.Path, s.roots)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, opErr(OpFileList, "%v", err)
	}

	out := make([]FileEntry, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		fe := FileEntry{
			Name:    e.Name(),
			Size:    info.Size(),
			Mode:    info.Mode().Perm().String(),
			IsDir:   e.IsDir(),
			ModTime: info.ModTime().Unix(),
		}
		fe.Owner, fe.Group = ownerNames(info)
		out = append(out, fe)
	}
	return out, nil
}

// opFileTailLog liefert die letzten n Zeilen — für den Log-Viewer aus Phase 2.
func (s *Server) opFileTailLog(_ context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[TailParams](raw, OpFileTailLog)
	if err != nil {
		return nil, err
	}
	path, err := jail(p.Path, s.roots)
	if err != nil {
		return nil, err
	}
	lines := p.Lines
	if lines <= 0 || lines > 5000 {
		lines = 200
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, opErr(OpFileTailLog, "%v", err)
	}
	defer f.Close()

	// Ringpuffer: konstanter Speicher unabhängig von der Dateigröße.
	ring := make([]string, lines)
	n := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		ring[n%lines] = sc.Text()
		n++
	}
	if err := sc.Err(); err != nil {
		return nil, opErr(OpFileTailLog, "%v", err)
	}

	count := min(n, lines)
	out := make([]string, 0, count)
	for i := n - count; i < n; i++ {
		out = append(out, ring[i%lines])
	}
	return TextResult{Text: strings.Join(out, "\n")}, nil
}

// writeFileAtomic schreibt über eine temporäre Datei im selben Verzeichnis und
// benennt sie um. Ein Absturz mitten im Schreiben hinterlässt so nie eine halbe
// Nginx-Config.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".volt-*.tmp")
	if err != nil {
		return fmt.Errorf("temporäre datei in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op nach erfolgreichem Rename

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	// Erst auf Platte, dann umbenennen — sonst kann ein Stromausfall eine leere
	// Datei an den endgültigen Namen bringen.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// checkFileGroup lehnt reservierte Systemgruppen ab — dieselbe Sperrliste wie
// checkUsername für den Eigentümer. Ohne sie konnten file.chown/file.write/
// file.mkdir die Gruppe einer Datei (optional rekursiv) auf jede existierende
// Systemgruppe setzen, z. B. "root", während der Eigentümer längst gesperrt war.
func checkFileGroup(g string) error {
	if !reUsername.MatchString(g) {
		return fmt.Errorf("%w: gruppenname %q", errBadInput, g)
	}
	switch g {
	case "root", "daemon", "bin", "sys", "www-data", "nobody", "volt", "volt-agent",
		"sshd", "mysql", "systemd-network", "systemd-resolve":
		return fmt.Errorf("%w: %q ist eine reservierte systemgruppe", errNotAllow, g)
	}
	return nil
}

// applyOwner setzt Eigentümer und Gruppe über die Namensauflösung des Systems.
//
// Vertraut owner/group ungeprüft — Aufrufer mit unsicherer Eingabe (die
// FileManager-Operationen in dieser Datei sowie ops_chunks.go/ops_files_ext.go)
// prüfen vorher selbst über checkUsername/checkFileGroup. Intern vertrauenswürdige
// Aufrufer (ops_app.go, ops_htpasswd.go) übergeben bewusst "root" als
// Eigentümer — checkUsername hier fest einzubauen hätte genau das
// grundsätzlich verhindert.
func applyOwner(path, owner, group string, recursive bool) error {
	if owner == "" {
		return nil
	}

	u, err := user.Lookup(owner)
	if err != nil {
		return fmt.Errorf("benutzer %q: %w", owner, err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return fmt.Errorf("uid %q: %w", u.Uid, err)
	}

	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return fmt.Errorf("gid %q: %w", u.Gid, err)
	}
	if group != "" {
		g, err := user.LookupGroup(group)
		if err != nil {
			return fmt.Errorf("gruppe %q: %w", group, err)
		}
		if gid, err = strconv.Atoi(g.Gid); err != nil {
			return fmt.Errorf("gid %q: %w", g.Gid, err)
		}
	}

	if !recursive {
		return os.Lchown(path, uid, gid)
	}
	// Lchown statt Chown: einem Symlink zu folgen würde aus dem Jail führen.
	return filepath.Walk(path, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(p, uid, gid)
	})
}
