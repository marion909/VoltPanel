// Package webui bettet das gebaute Vue-Frontend ins Binary ein.
//
// Deshalb braucht der Zielserver kein Node: `make build` erzeugt web/dist,
// kopiert es hierher, und embed.FS macht daraus einen Teil des Binaries.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:dist
var dist embed.FS

// FS liefert das Frontend als http.FileSystem, mit dist/ als Wurzel.
func FS() (http.FileSystem, error) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, err
	}
	return http.FS(sub), nil
}

// Built sagt, ob ein echtes Frontend eingebettet ist. Ohne gebautes Frontend
// liegt hier nur der Platzhalter, und das Panel liefert einen Hinweis statt
// einer weißen Seite.
func Built() bool {
	f, err := dist.Open("dist/index.html")
	if err != nil {
		return false
	}
	defer f.Close()

	info, err := f.Stat()
	return err == nil && info.Size() > 512
}
