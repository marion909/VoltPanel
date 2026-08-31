package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
)

// maxChunkBytes ist die Blockgröße für Up- und Download.
//
// 4 MiB liegt deutlich unter dem 8-MiB-Deckel einer einzelnen Socket-Anfrage
// und lässt Platz für den base64-Aufschlag von einem Drittel.
const maxChunkBytes = 4 << 20

func (s *Server) opFileReadChunk(_ context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[ChunkParams](raw, OpFileReadChunk)
	if err != nil {
		return nil, err
	}
	path, err := jail(p.Path, s.roots)
	if err != nil {
		return nil, err
	}
	if p.Offset < 0 {
		return nil, opErr(OpFileReadChunk, "negativer versatz")
	}

	length := p.Length
	if length <= 0 || length > maxChunkBytes {
		length = maxChunkBytes
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, opErr(OpFileReadChunk, "%v", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, opErr(OpFileReadChunk, "%v", err)
	}
	if info.IsDir() {
		return nil, opErr(OpFileReadChunk, "%s ist ein verzeichnis", p.Path)
	}

	buf := make([]byte, length)
	n, err := f.ReadAt(buf, p.Offset)
	// ReadAt meldet EOF auch dann, wenn es noch Bytes geliefert hat — das ist
	// hier kein Fehler, sondern genau das Signal, das wir weitergeben wollen.
	if err != nil && err != io.EOF {
		return nil, opErr(OpFileReadChunk, "%v", err)
	}

	return ChunkResult{
		Data:      base64.StdEncoding.EncodeToString(buf[:n]),
		EOF:       p.Offset+int64(n) >= info.Size(),
		Size:      info.Size(),
		BytesRead: n,
	}, nil
}

// opFileWriteChunk schreibt einen Block an eine Position in der Datei.
//
// Truncate im ersten Block leert eine eventuell vorhandene Datei; die folgenden
// Blöcke hängen an. So entsteht der Upload Stück für Stück, ohne dass der
// Web-Prozess die ganze Datei im Speicher halten muss.
func (s *Server) opFileWriteChunk(_ context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[ChunkParams](raw, OpFileWriteChunk)
	if err != nil {
		return nil, err
	}
	path, err := jail(p.Path, s.roots)
	if err != nil {
		return nil, err
	}
	if p.Offset < 0 {
		return nil, opErr(OpFileWriteChunk, "negativer versatz")
	}

	data, err := base64.StdEncoding.DecodeString(p.Data)
	if err != nil {
		return nil, opErr(OpFileWriteChunk, "daten sind kein gültiges base64")
	}
	if len(data) > maxChunkBytes {
		return nil, opErr(OpFileWriteChunk, "block ist %d bytes groß, maximum sind %d",
			len(data), maxChunkBytes)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, opErr(OpFileWriteChunk, "verzeichnis anlegen: %v", err)
	}

	flags := os.O_CREATE | os.O_WRONLY
	if p.Truncate {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return nil, opErr(OpFileWriteChunk, "%v", err)
	}
	defer f.Close()

	if _, err := f.WriteAt(data, p.Offset); err != nil {
		return nil, opErr(OpFileWriteChunk, "%v", err)
	}
	if err := f.Sync(); err != nil {
		return nil, opErr(OpFileWriteChunk, "%v", err)
	}

	// Der Eigentümer wird bei jedem Block gesetzt: Der erste legt die Datei an,
	// und ein späterer Fehler soll keine Datei zurücklassen, die root gehört.
	if p.Owner != "" {
		if err := applyOwner(path, p.Owner, "", false); err != nil {
			return nil, opErr(OpFileWriteChunk, "%v", err)
		}
	}

	info, err := f.Stat()
	if err != nil {
		return nil, opErr(OpFileWriteChunk, "%v", err)
	}
	return ChunkResult{Size: info.Size(), BytesRead: len(data)}, nil
}
