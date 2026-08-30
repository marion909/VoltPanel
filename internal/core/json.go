package core

import (
	"encoding/json"
	"io"
)

// decodeJSON liest höchstens 1 MiB — Release-Metadaten sind winzig, und ein
// bösartiger Update-Server soll den Speicher nicht füllen können.
func decodeJSON(r io.Reader, v any) error {
	return json.NewDecoder(io.LimitReader(r, 1<<20)).Decode(v)
}
