package agent

import (
	"context"
	"regexp"
	"strings"
	"time"
)

// Warum ein Dienst nicht startet.
//
// `systemctl restart` beantwortet das nicht. Es sagt
//
//	Job for pure-ftpd.service failed because the control process exited with
//	error code. See "systemctl status pure-ftpd.service" and
//	"journalctl -xeu pure-ftpd.service" for details.
//
// und verweist damit auf zwei Befehle, die derjenige, der die Meldung im Panel
// liest, gar nicht ausführen kann — er hat keine Shell auf dem Server. Die
// Meldung nennt also genau das nicht, was sie soll.
//
// Deshalb holt der Agent den Grund selbst, wenn ein Start scheitert.

// reJournalLine zerlegt eine Zeile im Format `journalctl -o short`:
//
//	Sep 01 07:12:03 web01 pure-ftpd-wrapper[5123]: "137:027" not two octal numbers
//
// Der Absender ist der Punkt der Übung. Was `systemd` sagt, ist Buchhaltung
// über den Fehlschlag; was der Dienst selbst sagt, ist der Fehler.
var reJournalLine = regexp.MustCompile(
	`^[A-Za-z]{3} +\d{1,2} \d\d:\d\d:\d\d \S+ ([A-Za-z0-9_.@:/-]+?)(?:\[\d+\])?: (.*)$`)

// systemdGeschwaetz sind die Zeilen, die systemd bei jedem Fehlschlag schreibt.
// Sie sagen, dass etwas schiefging — nie, was.
var systemdGeschwaetz = []string{
	"Starting ", "Started ", "Stopping ", "Stopped ",
	"Scheduled restart job", "Consumed ", "Deactivated successfully",
	"Succeeded.", "Failed with result", "Failed to start", "entered failed state",
}

// serviceFailure liefert den Grund aus dem Journal des Dienstes.
//
// Der Name geht durch checkService: die Whitelist der Units gilt auch für das
// Lesen. Das Journal eines fremden Dienstes kann Pfade, Adressen und
// Fehlermeldungen enthalten, die niemanden im Panel etwas angehen.
//
// since grenzt auf den einen Startversuch ein. Ohne das stehen in den letzten
// Zeilen die Versuche der letzten Tage, viermal dasselbe untereinander.
func (s *Server) serviceFailure(ctx context.Context, unit string, since time.Time) string {
	if err := checkService(unit); err != nil {
		return ""
	}

	// Kein Prioritätsfilter. systemd protokolliert die Ausgabe eines Dienstes —
	// auch die nach stderr — mit der Priorität aus SyslogLevel=, und die steht
	// voreingestellt auf "info". Ein Filter auf err..warning lässt damit genau
	// die Zeile weg, wegen der man nachsieht, und behält systemds eigenes
	// "Failed with result 'exit-code'". Das war hier der Fall.
	out, _ := run(ctx, shortTimeout, "journalctl", journalArgs(unit, since)...)
	if reason := interestingLines(out); reason != "" {
		return reason
	}

	// Ohne Journal (Container ohne systemd-journald) hilft der Status.
	out, _ = run(ctx, shortTimeout, "systemctl", "status", unit, "--no-pager", "--lines", "25")
	return interestingLines(out)
}

// journalArgs sind die Argumente, mit denen das Journal gelesen wird.
//
// Ausgelagert, damit die eine Entscheidung, die hier schiefging, prüfbar
// bleibt: kein --priority.
func journalArgs(unit string, since time.Time) []string {
	return []string{
		"--unit", unit,
		"--since", journalSince(since),
		"--lines", "60",
		"--no-pager",
		// short statt cat: der Absender jeder Zeile ist der Punkt der Übung.
		"--output", "short",
	}
}

// journalSince ist der Zeitpunkt, ab dem gelesen wird, in der Schreibweise, die
// journalctl versteht. Ortszeit — dieselbe, in der journalctl einen Zeitpunkt
// ohne Zone auslegt.
func journalSince(t time.Time) string {
	if t.IsZero() {
		t = time.Now().Add(-5 * time.Minute)
	}
	// Eine Sekunde zurück: journalctl vergleicht auf die Sekunde genau, und der
	// erste Eintrag eines Startversuchs trägt denselben Zeitstempel wie der
	// Aufruf, der ihn ausgelöst hat.
	return t.Add(-time.Second).Format("2006-01-02 15:04:05")
}

// interestingLines behält von einer Journal- oder Statusausgabe das, was den
// Fehler benennt.
//
// Zwei Töpfe. Was der Dienst selbst geschrieben hat, ist der Grund. Was systemd
// darüber notiert hat, ist Buchhaltung — brauchbar nur, solange der Dienst
// selbst nichts gesagt hat, und auch dann nur in Teilen: ein Abbruchcode
// ("status=203/EXEC") sagt etwas, ein "Failed to start" nichts.
func interestingLines(out string) string {
	var vomDienst, vonSystemd []string

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if m := reJournalLine.FindStringSubmatch(line); m != nil {
			absender, text := m[1], strings.TrimSpace(m[2])
			if text == "" {
				continue
			}
			if absender != "systemd" {
				vomDienst = append(vomDienst, text)
				continue
			}
			if !enthaeltEines(text, systemdGeschwaetz) {
				vonSystemd = append(vonSystemd, text)
			}
			continue
		}

		// Keine Journalzeile: der Kopf von `systemctl status`, oder eine
		// Markierung wie "-- No entries --".
		if statusGeruest(line) {
			continue
		}
		vonSystemd = append(vonSystemd, line)
	}

	keep := vomDienst
	if len(keep) == 0 {
		keep = vonSystemd
	}
	keep = ohneWiederholung(keep)
	if len(keep) == 0 {
		return ""
	}
	// Die letzten Zeilen: der Abbruch steht am Ende, nicht am Anfang.
	if len(keep) > 6 {
		keep = keep[len(keep)-6:]
	}
	return truncate(strings.Join(keep, " · "), 400)
}

// statusGeruest erkennt den Kopf von `systemctl status` und die Markierungen
// von journalctl. "Active:" und "Process:" bleiben stehen — dort steht der
// Abbruchcode.
func statusGeruest(line string) bool {
	switch {
	case strings.HasPrefix(line, "●"),
		strings.HasPrefix(line, "-- "),
		strings.HasPrefix(line, "Loaded:"),
		strings.HasPrefix(line, "Docs:"),
		strings.HasPrefix(line, "Main PID:"),
		strings.HasPrefix(line, "CPU:"),
		strings.HasPrefix(line, "Memory:"),
		strings.HasPrefix(line, "Tasks:"),
		strings.HasPrefix(line, "TasksMax:"),
		strings.HasPrefix(line, "CGroup:"),
		strings.HasPrefix(line, "└─"),
		strings.HasPrefix(line, "├─"):
		return true
	}
	return false
}

func enthaeltEines(text string, teile []string) bool {
	for _, t := range teile {
		if strings.Contains(text, t) {
			return true
		}
	}
	return false
}

// ohneWiederholung wirft doppelte Zeilen weg und behält die Reihenfolge.
//
// systemd versucht einen Start mehrfach; ohne das steht dieselbe Zeile viermal
// hintereinander in der Meldung und verdrängt alles andere.
func ohneWiederholung(lines []string) []string {
	gesehen := make(map[string]bool, len(lines))
	out := lines[:0:0]
	for _, l := range lines {
		if gesehen[l] {
			continue
		}
		gesehen[l] = true
		out = append(out, l)
	}
	return out
}

// startService startet einen Dienst und nennt bei einem Fehlschlag den Grund.
func (s *Server) startService(ctx context.Context, op Op, unit, action string) error {
	// Vor dem Versuch merken, damit das Journal danach nur diesen zeigt.
	since := time.Now()

	out, err := run(ctx, longTimeout, "systemctl", action, unit)
	if err == nil {
		return nil
	}
	msg := truncate(strings.TrimSpace(out), 200)
	if reason := s.serviceFailure(ctx, unit, since); reason != "" {
		msg += " — " + reason
	}
	return opErr(op, "%s %s: %s", unit, action, msg)
}
