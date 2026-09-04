package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
)

// Traffic aus den Nginx-Access-Logs.
//
// Gelesen wird ab einem Lesestand, nicht von vorn: eine Access-Log-Datei einer
// belebten Site hat nach einem Tag Millionen Zeilen, und jede Stunde alles neu
// zu lesen hiesse, jede Zeile 24-mal zu zählen.
//
// Der Lesestand steht im Panel, nicht hier — der Agent hält keinen Zustand über
// eine Operation hinaus. Er bekommt ihn mit und gibt den neuen zurück.

const (
	// Eine Zeile im Access-Log ist lang, aber nicht beliebig lang. Was darüber
	// liegt, ist keine Logzeile mehr.
	trafficMaxLine = 64 << 10
)

// trafficMaxRead ist, wie viel je Site und Lauf höchstens gelesen wird. Bei
// stündlichem Lauf sind 256 MB Log eine sehr belebte Site; darüber wird
// abgeschnitten und beim nächsten Mal weitergelesen, statt den Speicher
// vollaufen zu lassen.
//
// Als Variable, damit der Test das Weiterlesen an einer kleinen Datei prüfen
// kann statt an einer von 256 MB. Verändert wird sie ausschliesslich dort.
var trafficMaxRead int64 = 256 << 20

// reAccessLog liest das combined-Format und die beiden Zahlen, die VoltPanel
// anhängt.
//
// Beide Formate in einem Ausdruck, weil auf einem Server, der schon lief, noch
// Zeilen im alten Format stehen: nach einer Aktualisierung wird die
// nginx-Konfiguration neu geschrieben, aber die Datei behält ihren Anfang.
//
// Ohne die beiden Zahlen bleibt $body_bytes_sent — der Rumpf der Antwort ohne
// Kopfzeilen. Das ist zu wenig, aber es ist die ehrlichste Zahl, die in einer
// solchen Zeile steht.
var reAccessLog = regexp.MustCompile(
	`^\S+ \S+ \S+ \[[^\]]*\] "[^"]*" (\d{3}) (\d+) "[^"]*" "[^"]*"(?: (\d+) (\d+))?\s*$`)

// TrafficCursor ist der Lesestand einer Logdatei.
type TrafficCursor struct {
	Domain string `json:"domain"`
	Offset int64  `json:"offset"`
	// Inode erkennt die Rotation. 0 heisst "noch nie gelesen".
	Inode uint64 `json:"inode"`
}

type TrafficParams struct {
	Files []TrafficCursor `json:"files"`
}

// TrafficCount ist das Ergebnis für eine Site.
type TrafficCount struct {
	Domain   string `json:"domain"`
	Bytes    int64  `json:"bytes"`
	Requests int64  `json:"requests"`
	// Offset und Inode sind der neue Lesestand. Sie gehören auch dann
	// gespeichert, wenn Bytes 0 ist — sonst wird beim nächsten Lauf dieselbe
	// Stelle noch einmal gelesen.
	Offset int64  `json:"offset"`
	Inode  uint64 `json:"inode"`
	// Rotated sagt, dass die Datei zwischen zwei Läufen ausgetauscht wurde.
	Rotated bool `json:"rotated"`
	// Skipped zählt Zeilen, die zu keinem Format passten. Ein grosser Wert
	// heisst: das Format stimmt nicht mit dem überein, was hier erwartet wird.
	Skipped int64  `json:"skipped"`
	Error   string `json:"error"`
}

type TrafficResult struct {
	Files []TrafficCount `json:"files"`
}

// opNginxTraffic liest die Access-Logs ab dem übergebenen Stand.
//
// Der Pfad wird aus dem Logverzeichnis des Agents und der geprüften Domain
// gebaut. Käme er aus der Anfrage, wäre "zähle den Traffic" ein Weg, jede Datei
// des Servers zeilenweise auszulesen — und die Antwort verriete über die Zahl
// der passenden Zeilen genug, um das auszunutzen.
func (s *Server) opNginxTraffic(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[TrafficParams](raw, OpNginxTraffic)
	if err != nil {
		return nil, err
	}
	if len(p.Files) > 500 {
		return nil, opInputErr(OpNginxTraffic, "höchstens 500 sites auf einmal")
	}

	res := TrafficResult{Files: make([]TrafficCount, 0, len(p.Files))}
	for _, cursor := range p.Files {
		if err := ctx.Err(); err != nil {
			break
		}
		count := TrafficCount{Domain: cursor.Domain, Offset: cursor.Offset, Inode: cursor.Inode}

		domain, err := checkDomain(cursor.Domain)
		if err != nil {
			count.Error = err.Error()
			res.Files = append(res.Files, count)
			continue
		}
		// Normalisiert weiterreichen: readTraffic baut daraus einen Dateipfad
		// (s.logDir/<domain>.access.log). Ohne das könnten "Example.com" und
		// "example.com" auf einem case-sensitiven Dateisystem zwei getrennte
		// Logdateien treffen, obwohl beide dieselbe Site meinen.
		cursor.Domain, count.Domain = domain, domain
		if err := s.readTraffic(&count, cursor); err != nil {
			count.Error = err.Error()
		}
		res.Files = append(res.Files, count)
	}
	return res, nil
}

// readTraffic liest eine Logdatei ab dem Lesestand und zählt.
func (s *Server) readTraffic(count *TrafficCount, cursor TrafficCursor) error {
	path := filepath.Join(s.logDir, cursor.Domain+".access.log")
	// Auch der abgeleitete Pfad geht durch die Wurzelprüfung. Eine Domain, die
	// checkDomain passiert, kann zwar kein ".." enthalten — aber ein Symlink im
	// Logverzeichnis kann trotzdem woanders hinzeigen.
	path, err := jail(path, s.roots)
	if err != nil {
		return err
	}

	f, err := os.Open(path)
	if os.IsNotExist(err) {
		// Eine Site ohne Zugriffe hat noch keine Logdatei. Kein Fehler.
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}
	inode := inodeOf(info)

	start := cursor.Offset
	switch {
	case cursor.Inode != 0 && inode != cursor.Inode:
		// Rotiert. Was seit dem letzten Lauf noch in die alte Datei ging, holt
		// readRotated nach — sonst fehlte der Traffic zwischen dem letzten Lauf
		// und der Rotation, also bis zu einer Stunde am Tag.
		count.Rotated = true
		s.readRotated(path, cursor.Offset, count)
		start = 0
	case info.Size() < cursor.Offset:
		// Nicht rotiert, aber kleiner: die Datei wurde gekürzt (copytruncate).
		// Von vorn zu lesen ist die einzige Wahl, die nichts erfindet.
		count.Rotated = true
		start = 0
	}

	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return err
	}
	read, err := countTraffic(f, count, trafficMaxRead)
	count.Offset = start + read
	count.Inode = inode
	return err
}

// readRotated holt den Rest aus der eben rotierten Datei.
//
// logrotate legt auf Debian ".1" an und komprimiert erst beim übernächsten
// Lauf (delaycompress). Bei stündlicher Messung und täglicher Rotation ist die
// Datei also noch lesbar. Eine bereits gepackte .1.gz wird nicht angefasst —
// dann fehlt eine Stunde, und das ist ehrlicher als ein Zähler, der beim
// Auspacken von Megabyte hängt.
func (s *Server) readRotated(path string, offset int64, count *TrafficCount) {
	f, err := os.Open(path + ".1")
	if err != nil {
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.Size() <= offset {
		return
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return
	}
	_, _ = countTraffic(f, count, trafficMaxRead)
}

// countTraffic zählt die Zeilen eines Ausschnitts und gibt zurück, wie viele
// Bytes davon gelesen wurden.
//
// Zwei Dinge daran sind wichtiger, als sie aussehen.
//
// **Nur vollständige Zeilen zählen.** nginx schreibt weiter, während gelesen
// wird; am Ende steht regelmässig eine halbe Zeile. Sie zu zählen hiesse, sie
// beim nächsten Lauf ein zweites Mal — dann vollständig — zu zählen. Ohne
// Zeilenumbruch wird deshalb nichts gezählt und der Lesestand nicht bewegt.
//
// **Der Rückgabewert ist das Gelesene, nicht die Dateigrösse.** Greift die
// Obergrenze, steht der neue Lesestand mitten in der Datei, und der nächste
// Lauf macht dort weiter. Aus der Dateigrösse gebildet, übersprünge er alles
// dazwischen — bei einer belebten Site jede Stunde aufs Neue.
func countTraffic(r io.Reader, count *TrafficCount, limit int64) (int64, error) {
	br := bufio.NewReaderSize(io.LimitReader(r, limit), trafficMaxLine)

	var read int64
	for {
		line, err := br.ReadSlice('\n')

		switch {
		case err == nil:
			read += int64(len(line))
			countLine(line[:len(line)-1], count)

		case errors.Is(err, bufio.ErrBufferFull):
			// Länger als jede Logzeile sein kann. Verwerfen und bis zum
			// nächsten Umbruch weiterlesen — sonst bliebe der Zähler hier
			// für immer stehen.
			read += int64(len(line))
			count.Skipped++
			n, done := discardLine(br)
			read += n
			if done {
				return read, nil
			}

		default:
			// EOF oder Lesefehler. Was noch im Puffer steht, ist eine
			// unvollständige Zeile und bleibt für den nächsten Lauf liegen.
			return read, nil
		}
	}
}

// discardLine liest bis zum nächsten Zeilenumbruch und sagt, ob die Eingabe
// dabei zu Ende ging.
func discardLine(br *bufio.Reader) (int64, bool) {
	var n int64
	for {
		line, err := br.ReadSlice('\n')
		n += int64(len(line))
		if err == nil {
			return n, false
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return n, true
		}
	}
}

// countLine wertet eine einzelne Zeile aus.
func countLine(line []byte, count *TrafficCount) {
	m := reAccessLog.FindSubmatch(line)
	if m == nil {
		count.Skipped++
		return
	}
	count.Requests++

	// Die beiden angehängten Zahlen, wenn vorhanden: Anfrage plus
	// vollständige Antwort.
	if len(m[3]) > 0 && len(m[4]) > 0 {
		in, _ := strconv.ParseInt(string(m[3]), 10, 64)
		out, _ := strconv.ParseInt(string(m[4]), 10, 64)
		count.Bytes += in + out
		return
	}
	// Sonst nur der Rumpf der Antwort aus dem combined-Format.
	body, _ := strconv.ParseInt(string(m[2]), 10, 64)
	count.Bytes += body
}

// inodeOf liefert die Inode-Nummer. Auf Systemen ohne Stat_t (Windows) gibt es
// keine — dort greift die Grössenprüfung.
func inodeOf(info os.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Ino)
	}
	return 0
}
