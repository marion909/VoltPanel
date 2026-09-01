package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Echte Zeilen, wie nginx sie schreibt.
const (
	// combined, das Vorgabeformat. Nur der Rumpf der Antwort steht darin.
	zeileCombined = `203.0.113.5 - - [01/Sep/2026:06:00:00 +0200] "GET /index.html HTTP/1.1" ` +
		`200 1234 "-" "Mozilla/5.0 (X11; Linux x86_64)"`

	// Das Format von VoltPanel: combined plus Anfrage- und Antwortgrösse.
	zeileVolt = `203.0.113.5 - - [01/Sep/2026:06:00:01 +0200] "GET /bild.png HTTP/1.1" ` +
		`200 50000 "https://example.at/" "Mozilla/5.0" 412 50288`
)

// TestTrafficZaehltBeideFormate: nach einer Aktualisierung wird die
// nginx-Konfiguration neu geschrieben, die Logdatei behält aber ihren Anfang.
// In derselben Datei stehen dann beide Formate.
func TestTrafficZaehltBeideFormate(t *testing.T) {
	var count TrafficCount
	read, err := countTraffic(strings.NewReader(zeileCombined+"\n"+zeileVolt+"\n"), &count, trafficMaxRead)
	if err != nil {
		t.Fatal(err)
	}

	if count.Requests != 2 {
		t.Errorf("Requests = %d, erwartet 2", count.Requests)
	}
	if count.Skipped != 0 {
		t.Errorf("%d Zeilen übersprungen, erwartet 0", count.Skipped)
	}

	// combined liefert 1234 (nur der Rumpf), die Volt-Zeile 412 + 50288.
	want := int64(1234 + 412 + 50288)
	if count.Bytes != want {
		t.Errorf("Bytes = %d, erwartet %d", count.Bytes, want)
	}

	// Der Lesestand muss aus dem Gelesenen entstehen, nicht aus der Dateigrösse.
	wantRead := int64(len(zeileCombined) + 1 + len(zeileVolt) + 1)
	if read != wantRead {
		t.Errorf("gelesen = %d bytes, erwartet %d", read, wantRead)
	}
}

// TestTrafficZaehltNichtsAusFremdenZeilen: was nicht wie eine Logzeile
// aussieht, darf keine Bytes beitragen. Sonst liesse sich über eine Anfrage
// mit passendem Pfad die eigene Traffic-Zahl beeinflussen.
func TestTrafficZaehltNichtsAusFremdenZeilen(t *testing.T) {
	fremd := strings.Join([]string{
		"",
		"nur text",
		// Eine Anfrage, deren Pfad selbst wie eine Logzeile aussieht. Der Pfad
		// steht in Anführungszeichen und kann den Ausdruck nicht verlassen.
		`203.0.113.5 - - [01/Sep/2026:06:00:02 +0200] ` +
			`"GET /x\" 200 999999999 \"-\" \"-\" 1 999999999 HTTP/1.1" 404 5 "-" "-" 100 200`,
		// Fehlerlogzeile, versehentlich im selben Verzeichnis.
		`2026/09/01 06:00:03 [error] 1234#0: *5 open() failed`,
	}, "\n") + "\n"

	var count TrafficCount
	if _, err := countTraffic(strings.NewReader(fremd), &count, trafficMaxRead); err != nil {
		t.Fatal(err)
	}
	if count.Bytes > 300 {
		t.Errorf("aus fremden Zeilen wurden %d Bytes gezählt", count.Bytes)
	}
	if count.Skipped < 3 {
		t.Errorf("nur %d Zeilen übersprungen — der Ausdruck ist zu weit gefasst", count.Skipped)
	}
}

// TestTrafficLiestNurAbDemStand ist der Grund für den Lesestand: eine Logdatei
// wächst, und jede Stunde alles neu zu lesen hiesse, jede Zeile 24-mal zu
// zählen.
func TestTrafficLiestNurAbDemStand(t *testing.T) {
	srv, dir := testServer(t)
	logDir := filepath.Join(dir, "log")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	srv.logDir = logDir
	srv.roots = append(srv.roots, logDir)

	pfad := filepath.Join(logDir, "example.at.access.log")
	if err := os.WriteFile(pfad, []byte(zeileVolt+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	lauf := func(cursor TrafficCursor) TrafficCount {
		t.Helper()
		raw, _ := json.Marshal(TrafficParams{Files: []TrafficCursor{cursor}})
		out, err := srv.opNginxTraffic(context.Background(), raw)
		if err != nil {
			t.Fatal(err)
		}
		res := out.(TrafficResult)
		if len(res.Files) != 1 {
			t.Fatalf("%d Ergebnisse, erwartet 1", len(res.Files))
		}
		if res.Files[0].Error != "" {
			t.Fatalf("Fehler: %s", res.Files[0].Error)
		}
		return res.Files[0]
	}

	erst := lauf(TrafficCursor{Domain: "example.at"})
	if erst.Requests != 1 {
		t.Fatalf("erster Lauf: %d Anfragen, erwartet 1", erst.Requests)
	}

	// Zweiter Lauf ohne neue Zeilen: nichts dazu.
	zweit := lauf(TrafficCursor{Domain: "example.at", Offset: erst.Offset, Inode: erst.Inode})
	if zweit.Requests != 0 || zweit.Bytes != 0 {
		t.Errorf("zweiter Lauf zählte erneut: %d Anfragen, %d Bytes",
			zweit.Requests, zweit.Bytes)
	}

	// Eine neue Zeile: genau die eine dazu.
	f, err := os.OpenFile(pfad, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(zeileCombined + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	dritt := lauf(TrafficCursor{Domain: "example.at", Offset: zweit.Offset, Inode: zweit.Inode})
	if dritt.Requests != 1 {
		t.Errorf("dritter Lauf: %d Anfragen, erwartet 1", dritt.Requests)
	}
	if dritt.Bytes != 1234 {
		t.Errorf("dritter Lauf: %d Bytes, erwartet 1234", dritt.Bytes)
	}
}

// TestTrafficUeberlebtDieRotation.
//
// logrotate tauscht die Datei aus. Ohne Erkennung läse der nächste Lauf ab
// einem Stand, den es in der neuen Datei nicht gibt — und zählte je nach Grösse
// entweder gar nichts mehr oder mitten in einer Zeile weiter.
func TestTrafficUeberlebtDieRotation(t *testing.T) {
	srv, dir := testServer(t)
	logDir := filepath.Join(dir, "log")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	srv.logDir = logDir
	srv.roots = append(srv.roots, logDir)

	pfad := filepath.Join(logDir, "example.at.access.log")
	if err := os.WriteFile(pfad, []byte(zeileVolt+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	lauf := func(cursor TrafficCursor) TrafficCount {
		t.Helper()
		raw, _ := json.Marshal(TrafficParams{Files: []TrafficCursor{cursor}})
		out, err := srv.opNginxTraffic(context.Background(), raw)
		if err != nil {
			t.Fatal(err)
		}
		return out.(TrafficResult).Files[0]
	}

	erst := lauf(TrafficCursor{Domain: "example.at"})

	// Zwischen den Läufen kommt noch eine Zeile dazu, dann wird rotiert.
	f, _ := os.OpenFile(pfad, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(zeileCombined + "\n")
	f.Close()

	if err := os.Rename(pfad, pfad+".1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pfad, []byte(zeileVolt+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	zweit := lauf(TrafficCursor{Domain: "example.at", Offset: erst.Offset, Inode: erst.Inode})
	if !zweit.Rotated {
		t.Error("die Rotation wurde nicht erkannt")
	}
	// Die nachgeschobene Zeile aus der alten Datei plus die neue.
	if zweit.Requests != 2 {
		t.Errorf("nach der Rotation: %d Anfragen, erwartet 2 (eine aus .1, eine neu)",
			zweit.Requests)
	}
	if zweit.Inode == erst.Inode {
		t.Error("der neue Lesestand trägt noch die alte Inode")
	}
}

// TestTrafficNimmtNurGeprueteDomains: der Pfad wird aus dem Logverzeichnis und
// der Domain gebaut. Ohne Prüfung wäre "zähle den Traffic" ein Weg, jede Datei
// des Servers zeilenweise auszulesen.
func TestTrafficNimmtNurGeprueteDomains(t *testing.T) {
	srv, dir := testServer(t)

	// Das Logverzeichnis muss existieren und eine Wurzel sein. Sonst lehnt
	// jail() ohnehin jeden Pfad ab, und der Test wäre grün, auch wenn die
	// Domainprüfung fehlte — genau so war er beim ersten Versuch.
	logDir := filepath.Join(dir, "log")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	srv.logDir = logDir
	srv.roots = append(srv.roots, logDir)

	// Eine Datei, die es gibt: ohne sie liefe jeder Aufruf ins
	// "gibt es nicht" und der Test sagte wieder nichts.
	if err := os.WriteFile(filepath.Join(logDir, ".access.log"),
		[]byte(zeileVolt+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	böse := []string{
		"../../etc/passwd",
		"/etc/shadow",
		"example.at/../../../etc/passwd",
		"",
		"example.at\x00",
	}
	raw, _ := json.Marshal(TrafficParams{Files: cursorsFor(böse)})
	out, err := srv.opNginxTraffic(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range out.(TrafficResult).Files {
		if f.Error == "" {
			t.Errorf("die domain %q wurde angenommen", f.Domain)
		}
		if f.Bytes != 0 || f.Requests != 0 {
			t.Errorf("für %q wurde trotzdem gezählt", f.Domain)
		}
	}
}

func cursorsFor(domains []string) []TrafficCursor {
	out := make([]TrafficCursor, 0, len(domains))
	for _, d := range domains {
		out = append(out, TrafficCursor{Domain: d})
	}
	return out
}

// TestUnvollstaendigeZeileWirdNichtGezaehlt.
//
// nginx schreibt weiter, während gelesen wird; am Ende steht regelmässig eine
// halbe Zeile. Sie zu zählen hiesse, den Lesestand über sie hinweg zu setzen —
// und beim nächsten Lauf käme sie vollständig noch einmal, also doppelt.
func TestUnvollstaendigeZeileWirdNichtGezaehlt(t *testing.T) {
	// Zwei ganze Zeilen und ein angefangener Rest.
	eingabe := zeileVolt + "\n" + zeileCombined + "\n" + `203.0.113.9 - - [01/Sep`

	var count TrafficCount
	read, err := countTraffic(strings.NewReader(eingabe), &count, trafficMaxRead)
	if err != nil {
		t.Fatal(err)
	}
	if count.Requests != 2 {
		t.Errorf("Requests = %d, erwartet 2 — die halbe Zeile wurde mitgezählt", count.Requests)
	}
	ganz := int64(len(zeileVolt) + 1 + len(zeileCombined) + 1)
	if read != ganz {
		t.Errorf("Lesestand = %d, erwartet %d — er steht mitten in der letzten Zeile",
			read, ganz)
	}
}

// TestLeseobergrenzeBewegtDenStandNurSoweitGelesen.
//
// Bei einer sehr belebten Site wird abgeschnitten und beim nächsten Lauf
// weitergelesen. Käme der neue Lesestand aus der Dateigrösse statt aus dem
// Gelesenen, wäre alles dazwischen für immer übersprungen.
func TestLeseobergrenzeBewegtDenStandNurSoweitGelesen(t *testing.T) {
	eingabe := zeileVolt + "\n" + zeileVolt + "\n" + zeileVolt + "\n"
	eineZeile := int64(len(zeileVolt) + 1)

	var count TrafficCount
	// Grenze mitten in der zweiten Zeile.
	read, err := countTraffic(strings.NewReader(eingabe), &count, eineZeile+10)
	if err != nil {
		t.Fatal(err)
	}
	if count.Requests != 1 {
		t.Errorf("Requests = %d, erwartet 1", count.Requests)
	}
	if read != eineZeile {
		t.Errorf("Lesestand = %d, erwartet %d", read, eineZeile)
	}

	// Und der Rest kommt beim nächsten Lauf.
	var weiter TrafficCount
	if _, err := countTraffic(strings.NewReader(eingabe[read:]), &weiter, trafficMaxRead); err != nil {
		t.Fatal(err)
	}
	if weiter.Requests != 2 {
		t.Errorf("zweiter Lauf: %d Anfragen, erwartet 2", weiter.Requests)
	}
}

// TestUebermaessigLangeZeileHaeltDenZaehlerNichtAuf: was länger ist als jede
// Logzeile sein kann, wird verworfen — der Zähler darf dort nicht stehen
// bleiben, sonst zählt diese Site nie wieder.
func TestUebermaessigLangeZeileHaeltDenZaehlerNichtAuf(t *testing.T) {
	monster := strings.Repeat("x", trafficMaxLine+100)
	eingabe := monster + "\n" + zeileVolt + "\n"

	var count TrafficCount
	read, err := countTraffic(strings.NewReader(eingabe), &count, trafficMaxRead)
	if err != nil {
		t.Fatal(err)
	}
	if count.Requests != 1 {
		t.Errorf("Requests = %d, erwartet 1 — die Zeile nach dem Monster fehlt", count.Requests)
	}
	if read != int64(len(eingabe)) {
		t.Errorf("Lesestand = %d, erwartet %d — das Monster wurde nicht übersprungen",
			read, len(eingabe))
	}
}

// TestLesestandKommtAusDemGelesenen prüft dieselbe Zusage wie oben, aber am
// vollständigen Weg — mit Datei, Lesestand und zweitem Lauf.
//
// Der Unterschied zwischen "so viel wurde gelesen" und "so gross ist die Datei"
// fällt nur auf, wenn die Obergrenze greift. Deshalb wird sie hier
// heruntergesetzt: sonst wäre der Test grün, egal welche der beiden Zahlen im
// Lesestand landet — und genau so war er beim ersten Versuch.
func TestLesestandKommtAusDemGelesenen(t *testing.T) {
	srv, dir := testServer(t)
	logDir := filepath.Join(dir, "log")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	srv.logDir = logDir
	srv.roots = append(srv.roots, logDir)

	pfad := filepath.Join(logDir, "example.at.access.log")
	inhalt := zeileVolt + "\n" + zeileVolt + "\n" + zeileVolt + "\n"
	if err := os.WriteFile(pfad, []byte(inhalt), 0o644); err != nil {
		t.Fatal(err)
	}

	alt := trafficMaxRead
	trafficMaxRead = int64(len(zeileVolt) + 11) // mitten in der zweiten Zeile
	t.Cleanup(func() { trafficMaxRead = alt })

	lauf := func(cursor TrafficCursor) TrafficCount {
		t.Helper()
		raw, _ := json.Marshal(TrafficParams{Files: []TrafficCursor{cursor}})
		out, err := srv.opNginxTraffic(context.Background(), raw)
		if err != nil {
			t.Fatal(err)
		}
		return out.(TrafficResult).Files[0]
	}

	erst := lauf(TrafficCursor{Domain: "example.at"})
	if erst.Requests != 1 {
		t.Fatalf("erster Lauf: %d Anfragen, erwartet 1", erst.Requests)
	}
	if erst.Offset != int64(len(zeileVolt)+1) {
		t.Errorf("Lesestand = %d, erwartet %d — er kommt aus der Dateigrösse "+
			"statt aus dem Gelesenen", erst.Offset, len(zeileVolt)+1)
	}

	// Der Rest kommt beim nächsten Lauf, nichts geht verloren.
	trafficMaxRead = alt
	zweit := lauf(TrafficCursor{Domain: "example.at", Offset: erst.Offset, Inode: erst.Inode})
	if zweit.Requests != 2 {
		t.Errorf("zweiter Lauf: %d Anfragen, erwartet 2", zweit.Requests)
	}
	if gesamt := erst.Requests + zweit.Requests; gesamt != 3 {
		t.Errorf("insgesamt %d Anfragen gezählt, in der Datei stehen 3", gesamt)
	}
}
