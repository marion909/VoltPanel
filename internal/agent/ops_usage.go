package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// DiskUsageParams fragt den Verbrauch eines Verzeichnisses ab.
type DiskUsageParams struct {
	Path string `json:"path"`
}

type DiskUsageResult struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
	Files int64  `json:"files"`
	Dirs  int64  `json:"dirs"`
}

// opDiskUsage misst den Platzverbrauch eines Verzeichnisses.
//
// Gezählt wird der belegte Platz (Blöcke), nicht die nominelle Dateigröße —
// das ist es, was eine echte Dateisystem-Quota auch zählt, und der Unterschied
// ist bei vielen kleinen Dateien erheblich.
//
// Symlinks werden nicht verfolgt: sonst zählte ein Link auf /usr den halben
// Server zur Quota der Site.
func (s *Server) opDiskUsage(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[DiskUsageParams](raw, OpDiskUsage)
	if err != nil {
		return nil, err
	}
	path, err := jail(p.Path, s.roots)
	if err != nil {
		return nil, err
	}

	res := DiskUsageResult{Path: path}
	// Hardlinks nur einmal zählen — sonst erscheint ein Backup mit Hardlinks
	// um ein Vielfaches größer, als es belegt.
	seen := make(map[uint64]struct{})

	walkErr := filepath.WalkDir(path, func(sub string, d fs.DirEntry, err error) error {
		if err != nil {
			// Ein einzelnes unlesbares Verzeichnis darf die Messung nicht
			// abbrechen — sonst hat eine Site mit einem schrägen Unterordner
			// dauerhaft keinen Messwert.
			if errors.Is(err, os.ErrPermission) {
				return nil
			}
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if d.IsDir() {
			res.Dirs++
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		// Symlinks belegen nur ihren eigenen Eintrag.
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}

		blocks, inode, ok := diskBlocks(info)
		if ok {
			if _, dup := seen[inode]; dup {
				return nil
			}
			seen[inode] = struct{}{}
			res.Bytes += blocks
		} else {
			res.Bytes += info.Size()
		}
		res.Files++
		return nil
	})
	if walkErr != nil {
		if os.IsNotExist(walkErr) {
			// Ein noch nicht angelegtes Verzeichnis belegt nichts.
			return res, nil
		}
		return nil, opErr(OpDiskUsage, "%v", walkErr)
	}
	return res, nil
}
