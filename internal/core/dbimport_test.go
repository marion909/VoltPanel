package core

import (
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
)

// TestCopyUnpackedErkenntGzip prüft die Erkennung am Inhalt statt an der
// Endung. Der Dateiname kommt vom Browser und sagt nichts darüber, was
// wirklich in der Datei steht.
func TestCopyUnpackedErkenntGzip(t *testing.T) {
	sql := "CREATE TABLE t (id INT);\n"

	var packed bytes.Buffer
	gz := gzip.NewWriter(&packed)
	if _, err := gz.Write([]byte(sql)); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		in   []byte
	}{
		{"unkomprimiert", []byte(sql)},
		{"gzip", packed.Bytes()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			n, err := copyUnpacked(&out, bytes.NewReader(tc.in), maxImportBytes)
			if err != nil {
				t.Fatalf("copyUnpacked: %v", err)
			}
			if out.String() != sql {
				t.Errorf("herausgekommen ist %q, erwartet %q", out.String(), sql)
			}
			if n != int64(len(sql)) {
				t.Errorf("%d bytes gemeldet, erwartet %d", n, len(sql))
			}
		})
	}
}

// TestCopyUnpackedDeckeltEntpackteGroesse: die Grenze muss nach dem Auspacken
// greifen. Sonst wäre ein kleines gzip mit riesigem Inhalt ein Weg, die Platte
// vollzuschreiben — die Datei käme unter jeder Upload-Grenze durch.
func TestCopyUnpackedDeckeltEntpackteGroesse(t *testing.T) {
	var packed bytes.Buffer
	gz := gzip.NewWriter(&packed)
	if _, err := gz.Write(bytes.Repeat([]byte("a"), 1<<20)); err != nil {
		t.Fatal(err)
	}
	gz.Close()

	if packed.Len() >= 4096 {
		t.Fatalf("das gepackte Testmuster ist mit %d bytes zu groß für die Aussage", packed.Len())
	}

	var out bytes.Buffer
	written, err := copyUnpacked(&out, bytes.NewReader(packed.Bytes()), 4096)
	if err == nil {
		t.Fatal("die Grenze hat nicht gegriffen")
	}
	if !strings.Contains(err.Error(), "entpackt") {
		t.Errorf("die Meldung nennt den Grund nicht: %v", err)
	}
	if written > 4096 {
		t.Errorf("%d bytes geschrieben, obwohl bei 4096 Schluss sein sollte", written)
	}
}
