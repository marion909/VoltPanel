package store

import (
	"database/sql"
	"encoding/json"
	"strings"
)

// scanner deckt *sql.Row und *sql.Rows ab, damit Einzel- und Listen-Query
// dieselbe Scan-Funktion benutzen können.
type scanner interface {
	Scan(dest ...any) error
}

// isUnique erkennt einen Verstoß gegen einen UNIQUE-Index.
func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// affected macht aus "0 geänderte Zeilen" ein ErrNotFound. Ohne das würde ein
// UPDATE auf eine fremde ID stillschweigend als Erfolg durchgehen.
func affected(res sql.Result, err error) error {
	if err != nil {
		if isUnique(err) {
			return ErrConflict
		}
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// encodeList speichert String-Slices als JSON-Array (Aliase, Cert-Domains).
func encodeList(v []string) string {
	if len(v) == 0 {
		return "[]"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func decodeList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// nilIfEmpty verhindert, dass ein leerer optionaler Fremdschlüssel als 0
// gespeichert wird und damit ins Leere zeigt.
func nilIfEmpty(id *int64) any {
	if id == nil || *id == 0 {
		return nil
	}
	return *id
}
