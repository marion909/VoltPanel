package agent

import (
	"bufio"
	"io"
	"os"
	"strconv"
	"strings"
)

// Echte Dateisystem-Quotas.
//
// Bis hierher wirken die Grenzen des Panels auf Anwendungsebene: eine Aktion
// über der Quota wird abgelehnt. Ein Prozess, der am Panel vorbei schreibt —
// der PHP-Code der Site selbst, ein Upload über FTP, ein entpacktes Archiv —
// merkt davon nichts. Das kann nur der Kernel.
//
// Das Mittel heißt Project Quota: ein Verzeichnisbaum bekommt eine
// Projektnummer, der Kernel bucht jeden Block darauf, und die Grenze hängt an
// der Nummer. Das passt zu diesem Panel besser als die ältere User Quota, denn
// hier gehört ein Verzeichnis einem Mandanten — und ein Mandant mit fünf Sites
// hat fünf Systembenutzer, aber eine Grenze.
//
// Voraussetzung ist eine Mount-Option, die niemand nachträglich setzen kann,
// ohne das Dateisystem neu einzuhängen. Deshalb steht hier zuerst eine ehrliche
// Auskunft darüber, was der Server kann, und keine Automatik, die an /etc/fstab
// herumschreibt.

const procMounts = "/proc/mounts"

// mountEntry ist eine Zeile aus /proc/mounts.
type mountEntry struct {
	Device string
	Point  string
	FSType string
	Opts   []string
}

// hasOpt sagt, ob eine Mount-Option gesetzt ist.
func (m mountEntry) hasOpt(name string) bool {
	for _, o := range m.Opts {
		if o == name || strings.HasPrefix(o, name+"=") {
			return true
		}
	}
	return false
}

// projectQuota sagt, ob dieser Mount Project Quota führt.
//
// Beide Dateisysteme melden die Option in /proc/mounts als "prjquota", auch
// wenn XFS sie als "pquota" annimmt — der Kernel schreibt seinen eigenen Namen
// zurück. Auf "pquota" wird trotzdem geprüft: das kostet nichts und hängt nicht
// daran, dass diese Normalisierung so bleibt.
func (m mountEntry) projectQuota() bool {
	return m.hasOpt("prjquota") || m.hasOpt("pquota")
}

// unescapeMount macht die Oktal-Fluchtfolgen rückgängig, mit denen /proc/mounts
// Leerzeichen, Tabulatoren, Zeilenumbrüche und Backslashes in Pfaden schreibt.
//
// Ohne das endet ein Einhängepunkt namens "/mnt/mein platz" als
// "/mnt/mein\040platz", und der Pfadvergleich weiter unten findet ihn nie —
// die Quota landete auf dem darüberliegenden Dateisystem.
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func parseMounts(r io.Reader) []mountEntry {
	var out []mountEntry
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)

	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 {
			continue
		}
		out = append(out, mountEntry{
			Device: unescapeMount(f[0]),
			Point:  unescapeMount(f[1]),
			FSType: f[2],
			Opts:   strings.Split(f[3], ","),
		})
	}
	return out
}

func readMounts() []mountEntry {
	f, err := os.Open(procMounts)
	if err != nil {
		return nil
	}
	defer f.Close()
	return parseMounts(f)
}

// mountFor sucht den Einhängepunkt, unter dem ein Pfad wirklich liegt: den
// längsten, der auf ihn passt.
//
// Die Länge ist der Punkt. Auf einem Server mit "/" und "/var/www" liegt
// /var/www/kunde.de auf dem zweiten — wer den ersten Treffer nimmt, setzt die
// Quota auf dem falschen Dateisystem und wundert sich, dass nichts geschieht.
//
// Verglichen wird an Pfadgrenzen: "/var" ist kein Einhängepunkt von
// "/vartmp/x". Ein späterer Eintrag schlägt einen gleich langen früheren —
// /proc/mounts führt eine Überlagerung hinten.
func mountFor(path string, mounts []mountEntry) (mountEntry, bool) {
	best := -1
	for i, m := range mounts {
		if !underPath(path, m.Point) {
			continue
		}
		if best < 0 || len(m.Point) >= len(mounts[best].Point) {
			best = i
		}
	}
	if best < 0 {
		return mountEntry{}, false
	}
	return mounts[best], true
}

// underPath sagt, ob path unter oder gleich base liegt.
func underPath(path, base string) bool {
	if base == "/" {
		return strings.HasPrefix(path, "/")
	}
	base = strings.TrimSuffix(base, "/")
	return path == base || strings.HasPrefix(path, base+"/")
}
