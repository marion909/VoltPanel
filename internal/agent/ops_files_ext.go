package agent

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// maxArchiveBytes deckelt, was beim Entpacken herauskommen darf. Ohne Deckel
// füllt eine "zip bomb" die Platte des Servers.
const maxArchiveBytes = 2 << 30 // 2 GiB

func (s *Server) opFileMove(_ context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[FileMoveParams](raw, OpFileMove)
	if err != nil {
		return nil, err
	}
	// Beide Enden müssen im Gefängnis liegen — sonst wäre Verschieben der
	// bequemste Weg, eine Datei nach /etc zu bekommen.
	from, err := jail(p.From, s.roots)
	if err != nil {
		return nil, err
	}
	to, err := jail(p.To, s.roots)
	if err != nil {
		return nil, err
	}
	if from == to {
		return TextResult{Text: to}, nil
	}

	if !p.Overwrite {
		if _, err := os.Lstat(to); err == nil {
			return nil, opErr(OpFileMove, "%s existiert bereits", p.To)
		}
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return nil, opErr(OpFileMove, "zielverzeichnis: %v", err)
	}
	if err := os.Rename(from, to); err != nil {
		return nil, opErr(OpFileMove, "%v", err)
	}
	return TextResult{Text: to}, nil
}

func (s *Server) opFileCopy(_ context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[FileMoveParams](raw, OpFileCopy)
	if err != nil {
		return nil, err
	}
	from, err := jail(p.From, s.roots)
	if err != nil {
		return nil, err
	}
	to, err := jail(p.To, s.roots)
	if err != nil {
		return nil, err
	}

	info, err := os.Lstat(from)
	if err != nil {
		return nil, opErr(OpFileCopy, "%v", err)
	}
	// Ein Verzeichnis in sich selbst zu kopieren läuft endlos.
	if info.IsDir() && strings.HasPrefix(to+string(filepath.Separator), from+string(filepath.Separator)) {
		return nil, opErr(OpFileCopy, "%s liegt innerhalb von %s", p.To, p.From)
	}
	if !p.Overwrite {
		if _, err := os.Lstat(to); err == nil {
			return nil, opErr(OpFileCopy, "%s existiert bereits", p.To)
		}
	}

	if err := copyPath(from, to); err != nil {
		return nil, opErr(OpFileCopy, "%v", err)
	}
	return TextResult{Text: to}, nil
}

func (s *Server) opFileChmod(_ context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[FileChmodParams](raw, OpFileChmod)
	if err != nil {
		return nil, err
	}
	path, err := jail(p.Path, s.roots)
	if err != nil {
		return nil, err
	}

	// Nur die neun Rechte-Bits: setuid oder setgid aus dem Panel wäre eine
	// Rechteausweitung auf Zuruf.
	mode := os.FileMode(p.Mode) & 0o777
	if mode == 0 {
		return nil, opErr(OpFileChmod, "modus 0 würde die datei unzugänglich machen")
	}

	if !p.Recursive {
		if err := os.Chmod(path, mode); err != nil {
			return nil, opErr(OpFileChmod, "%v", err)
		}
		return TextResult{Text: path}, nil
	}

	err = filepath.Walk(path, func(sub string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// Symlinks überspringen: chmod würde dem Link folgen und damit
		// möglicherweise aus dem Gefängnis heraus wirken.
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		target := mode
		if info.IsDir() {
			// Verzeichnisse brauchen das Ausführungsrecht, sonst sind sie
			// nicht mehr betretbar.
			target |= (mode & 0o444) >> 2
		}
		return os.Chmod(sub, target)
	})
	if err != nil {
		return nil, opErr(OpFileChmod, "%v", err)
	}
	return TextResult{Text: path}, nil
}

func (s *Server) opFileStat(_ context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[FilePathParams](raw, OpFileStat)
	if err != nil {
		return nil, err
	}
	path, err := jail(p.Path, s.roots)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, opErr(OpFileStat, "%v", err)
	}
	res := StatResult{
		Path: path, Name: info.Name(), Size: info.Size(),
		Mode: info.Mode().Perm().String(), IsDir: info.IsDir(),
		ModTime: info.ModTime().Unix(),
	}
	res.Owner, res.Group = ownerNames(info)
	return res, nil
}

// opFileArchive packt Pfade in ein tar.gz oder zip.
func (s *Server) opFileArchive(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[ArchiveParams](raw, OpFileArchive)
	if err != nil {
		return nil, err
	}
	if len(p.Sources) == 0 {
		return nil, opErr(OpFileArchive, "keine quelle angegeben")
	}

	dest, err := jail(p.Dest, s.roots)
	if err != nil {
		return nil, err
	}
	sources := make([]string, 0, len(p.Sources))
	for _, src := range p.Sources {
		resolved, err := jail(src, s.roots)
		if err != nil {
			return nil, err
		}
		sources = append(sources, resolved)
	}

	f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, opErr(OpFileArchive, "zielarchiv: %v", err)
	}
	defer f.Close()

	switch {
	case strings.HasSuffix(dest, ".zip"):
		err = writeZip(ctx, f, sources)
	case strings.HasSuffix(dest, ".tar.gz"), strings.HasSuffix(dest, ".tgz"):
		err = writeTarGz(ctx, f, sources)
	default:
		return nil, opErr(OpFileArchive, "endung von %q wird nicht unterstützt (.tar.gz oder .zip)", p.Dest)
	}
	if err != nil {
		os.Remove(dest)
		return nil, opErr(OpFileArchive, "%v", err)
	}

	if p.Owner != "" {
		if err := checkUsername(p.Owner); err != nil {
			return nil, err
		}
	}
	if err := applyOwner(dest, p.Owner, "", false); err != nil {
		return nil, opErr(OpFileArchive, "%v", err)
	}
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	return map[string]any{"path": dest, "size_bytes": info.Size()}, nil
}

// opFileExtract entpackt ein Archiv.
//
// Jeder Eintrag wird einzeln gegen das Zielverzeichnis geprüft: ein Archiv mit
// "../../etc/cron.d/x" darf nicht außerhalb landen (Zip-Slip).
func (s *Server) opFileExtract(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[ExtractParams](raw, OpFileExtract)
	if err != nil {
		return nil, err
	}
	archive, err := jail(p.Archive, s.roots)
	if err != nil {
		return nil, err
	}
	dest, err := jail(p.Dest, s.roots)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dest, 0o750); err != nil {
		return nil, opErr(OpFileExtract, "zielverzeichnis: %v", err)
	}

	var count int
	switch {
	case strings.HasSuffix(archive, ".zip"):
		count, err = extractZip(ctx, archive, dest)
	case strings.HasSuffix(archive, ".tar.gz"), strings.HasSuffix(archive, ".tgz"):
		count, err = extractTarGz(ctx, archive, dest)
	default:
		return nil, opErr(OpFileExtract, "endung von %q wird nicht unterstützt (.tar.gz oder .zip)", p.Archive)
	}
	if err != nil {
		return nil, opErr(OpFileExtract, "%v", err)
	}

	if p.Owner != "" {
		if err := checkUsername(p.Owner); err != nil {
			return nil, err
		}
		if err := applyOwner(dest, p.Owner, "", true); err != nil {
			return nil, opErr(OpFileExtract, "%v", err)
		}
	}
	return map[string]any{"path": dest, "entries": count}, nil
}

// safeJoin setzt einen Archiv-Eintragsnamen ans Ziel und stellt sicher, dass er
// dort bleibt. Das ist die Abwehr gegen Zip-Slip.
func safeJoin(dest, name string) (string, error) {
	if strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("eintrag %q enthält ein nullbyte", name)
	}
	// Absolute Pfade und Laufwerksangaben im Archiv gibt es nicht.
	clean := filepath.Clean("/" + strings.ReplaceAll(name, `\`, "/"))
	target := filepath.Join(dest, clean)

	if target != dest && !strings.HasPrefix(target, dest+string(filepath.Separator)) {
		return "", fmt.Errorf("eintrag %q zeigt aus dem zielverzeichnis heraus", name)
	}
	return target, nil
}

func writeTarGz(ctx context.Context, w io.Writer, sources []string) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)

	for _, src := range sources {
		base := filepath.Dir(src)
		err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}

			rel, err := filepath.Rel(base, path)
			if err != nil {
				return err
			}
			link := ""
			if info.Mode()&os.ModeSymlink != 0 {
				if link, err = os.Readlink(path); err != nil {
					return err
				}
			}
			header, err := tar.FileInfoHeader(info, link)
			if err != nil {
				return err
			}
			header.Name = rel

			if err := tw.WriteHeader(header); err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}

			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(tw, f)
			return err
		})
		if err != nil {
			return err
		}
	}

	// tar vor gzip schließen, sonst fehlt der Abschluss im Archiv.
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func writeZip(ctx context.Context, w io.Writer, sources []string) error {
	zw := zip.NewWriter(w)

	for _, src := range sources {
		base := filepath.Dir(src)
		err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// zip kennt keine Symlinks in dem Sinne — sie werden ausgelassen,
			// statt ihr Ziel hineinzukopieren.
			if info.Mode()&os.ModeSymlink != 0 {
				return nil
			}

			rel, err := filepath.Rel(base, path)
			if err != nil {
				return err
			}
			header, err := zip.FileInfoHeader(info)
			if err != nil {
				return err
			}
			header.Name = filepath.ToSlash(rel)
			if info.IsDir() {
				header.Name += "/"
			} else {
				header.Method = zip.Deflate
			}

			entry, err := zw.CreateHeader(header)
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}

			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(entry, f)
			return err
		})
		if err != nil {
			return err
		}
	}
	return zw.Close()
}

func extractTarGz(ctx context.Context, archive, dest string) (int, error) {
	f, err := os.Open(archive)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return 0, fmt.Errorf("kein gzip-archiv: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var count int
	var written int64

	for {
		header, err := tr.Next()
		if err == io.EOF {
			return count, nil
		}
		if err != nil {
			return count, err
		}
		if ctx.Err() != nil {
			return count, ctx.Err()
		}

		target, err := safeJoin(dest, header.Name)
		if err != nil {
			return count, err
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o750); err != nil {
				return count, err
			}
		case tar.TypeReg:
			written += header.Size
			if written > maxArchiveBytes {
				return count, fmt.Errorf("archiv entpackt zu mehr als %d bytes", int64(maxArchiveBytes))
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return count, err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY,
				os.FileMode(header.Mode)&0o777)
			if err != nil {
				return count, err
			}
			if _, err := io.Copy(out, io.LimitReader(tr, header.Size)); err != nil {
				out.Close()
				return count, err
			}
			if err := out.Close(); err != nil {
				return count, err
			}
			count++
		default:
			// Symlinks, Gerätedateien und Hardlinks werden übersprungen: ein
			// Symlink aus einem hochgeladenen Archiv wäre ein Ausbruch mit Ansage.
			continue
		}
	}
}

func extractZip(ctx context.Context, archive, dest string) (int, error) {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return 0, fmt.Errorf("kein zip-archiv: %w", err)
	}
	defer zr.Close()

	var count int
	var written uint64
	for _, entry := range zr.File {
		if ctx.Err() != nil {
			return count, ctx.Err()
		}

		target, err := safeJoin(dest, entry.Name)
		if err != nil {
			return count, err
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o750); err != nil {
				return count, err
			}
			continue
		}

		written += entry.UncompressedSize64
		if written > maxArchiveBytes {
			return count, fmt.Errorf("archiv entpackt zu mehr als %d bytes", int64(maxArchiveBytes))
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return count, err
		}

		if err := extractZipEntry(entry, target); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// extractZipEntry ist ausgelagert, damit die Handles per defer geschlossen
// werden und nicht erst am Ende der Schleife über alle Einträge.
func extractZipEntry(entry *zip.File, target string) error {
	rc, err := entry.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	mode := entry.Mode().Perm()
	if mode == 0 {
		mode = 0o644
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, io.LimitReader(rc, maxArchiveBytes))
	return err
}

// copyPath kopiert eine Datei oder einen Verzeichnisbaum.
func copyPath(from, to string) error {
	info, err := os.Lstat(from)
	if err != nil {
		return err
	}

	if !info.IsDir() {
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(from)
			if err != nil {
				return err
			}
			return os.Symlink(target, to)
		}
		return copyRegular(from, to, info.Mode().Perm())
	}

	return filepath.Walk(from, func(path string, sub os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		target := filepath.Join(to, rel)

		switch {
		case sub.IsDir():
			return os.MkdirAll(target, sub.Mode().Perm())
		case sub.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		default:
			return copyRegular(path, target, sub.Mode().Perm())
		}
	})
}

func copyRegular(from, to string, mode os.FileMode) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(to), 0o750); err != nil {
		return err
	}
	out, err := os.OpenFile(to, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
