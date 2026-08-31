//go:build linux

package agent

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// clockTicks ist der Wert von sysconf(_SC_CLK_TCK). Er ist auf jedem
// Linux-System der letzten zwanzig Jahre 100; ihn über cgo abzufragen wäre der
// einzige Grund, cgo überhaupt einzubinden — und das Programm soll statisch
// gelinkt bleiben.
const clockTicks = 100

// readProcesses liest die Prozessliste direkt aus /proc.
//
// Nicht über `ps`: das wäre ein weiteres Programm, dessen Ausgabeformat je nach
// Distribution wechselt. /proc ist die Quelle, aus der ps selbst liest.
func readProcesses() ([]ProcessInfo, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}

	uptime, err := readUptime()
	if err != nil {
		return nil, err
	}

	pageSize := int64(os.Getpagesize())
	names := map[uint32]string{}
	out := make([]ProcessInfo, 0, 256)

	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || !e.IsDir() {
			continue
		}
		info, err := readProcess(pid, uptime, pageSize, names)
		if err != nil {
			// Prozesse enden zwischen ReadDir und dem Lesen — das ist der
			// Normalfall, kein Fehler.
			continue
		}
		out = append(out, *info)
	}
	return out, nil
}

func readUptime() (float64, error) {
	raw, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return 0, fmt.Errorf("/proc/uptime ist leer")
	}
	return strconv.ParseFloat(fields[0], 64)
}

func readProcess(pid int, uptime float64, pageSize int64, names map[uint32]string) (*ProcessInfo, error) {
	dir := filepath.Join("/proc", strconv.Itoa(pid))

	raw, err := os.ReadFile(filepath.Join(dir, "stat"))
	if err != nil {
		return nil, err
	}
	// Der Name in Klammern kann Leerzeichen und Klammern enthalten. Deshalb
	// wird ab der LETZTEN schließenden Klammer getrennt, nicht am ersten
	// Leerzeichen — sonst verschieben sich alle folgenden Felder.
	close := strings.LastIndex(string(raw), ")")
	if close < 0 || close+2 >= len(raw) {
		return nil, fmt.Errorf("stat von %d unlesbar", pid)
	}
	comm := string(raw[strings.Index(string(raw), "(")+1 : close])
	fields := strings.Fields(string(raw[close+2:]))
	if len(fields) < 20 {
		return nil, fmt.Errorf("stat von %d hat nur %d felder", pid, len(fields))
	}

	// Feldnummern nach proc(5), gezählt ab state = Feld 3.
	state := fields[0]
	ppid, _ := strconv.Atoi(fields[1])
	utime, _ := strconv.ParseFloat(fields[11], 64)
	stime, _ := strconv.ParseFloat(fields[12], 64)
	threads, _ := strconv.Atoi(fields[17])
	starttime, _ := strconv.ParseFloat(fields[19], 64)

	// Der Mittelwert über die Lebenszeit des Prozesses — dasselbe, was `ps`
	// unter %CPU zeigt. Ein Momentanwert bräuchte zwei Messungen im Abstand
	// von einer Sekunde; dafür ist eine Listenansicht der falsche Ort.
	var cpu float64
	if lifetime := uptime - starttime/clockTicks; lifetime > 0 {
		cpu = 100 * ((utime + stime) / clockTicks) / lifetime
	}

	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	uid := uint32(0)
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		uid = st.Uid
	}

	return &ProcessInfo{
		PID: pid, PPID: ppid, User: userName(uid, names), State: state,
		CPUPercent: round1(cpu), MemBytes: readRSS(dir) * pageSize,
		Threads: threads, Command: commandLine(dir, comm),
	}, nil
}

// readRSS liest die belegten Seiten aus statm. Der Wert in stat wäre derselbe,
// statm ist aber schmaler und muss nicht zerlegt werden.
func readRSS(dir string) int64 {
	raw, err := os.ReadFile(filepath.Join(dir, "statm"))
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(raw))
	if len(fields) < 2 {
		return 0
	}
	pages, _ := strconv.ParseInt(fields[1], 10, 64)
	return pages
}

// commandLine setzt die mit Nullbytes getrennte Kommandozeile zusammen. Kernel-
// Threads haben keine; für sie bleibt der Name aus stat.
func commandLine(dir, comm string) string {
	raw, err := os.ReadFile(filepath.Join(dir, "cmdline"))
	if err != nil || len(raw) == 0 {
		return "[" + comm + "]"
	}
	parts := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
	return truncate(strings.Join(parts, " "), 200)
}

// userName löst die UID auf und merkt sich das Ergebnis: eine Prozessliste
// enthält hunderte Einträge mit einer Handvoll verschiedener Benutzer.
func userName(uid uint32, cache map[uint32]string) string {
	if name, ok := cache[uid]; ok {
		return name
	}
	name := strconv.FormatUint(uint64(uid), 10)
	if u, err := user.LookupId(name); err == nil {
		name = u.Username
	}
	cache[uid] = name
	return name
}

func round1(v float64) float64 {
	return float64(int64(v*10+0.5)) / 10
}

// processOwner nennt den Benutzer, dem ein laufender Prozess gehört.
func processOwner(pid int) (string, error) {
	info, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid)))
	if err != nil {
		return "", fmt.Errorf("prozess %d läuft nicht", pid)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("eigentümer von %d nicht feststellbar", pid)
	}
	return userName(st.Uid, map[uint32]string{}), nil
}
