package agent

import (
	"context"
	"strings"
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

// serviceFailure liefert die letzten Journalzeilen eines Dienstes.
//
// Der Name geht durch checkService: die Whitelist der Units gilt auch für das
// Lesen. Der Journal eines fremden Dienstes kann Pfade, Adressen und
// Fehlermeldungen enthalten, die niemanden im Panel etwas angehen.
func (s *Server) serviceFailure(ctx context.Context, unit string) string {
	if err := checkService(unit); err != nil {
		return ""
	}

	out, err := run(ctx, shortTimeout, "journalctl", "--unit", unit,
		"--lines", "25", "--no-pager", "--output", "cat", "--priority", "err..warning")
	if err != nil || strings.TrimSpace(out) == "" {
		// Ohne Journal (Container ohne systemd-journald) hilft der Status.
		out, _ = run(ctx, shortTimeout, "systemctl", "status", unit,
			"--no-pager", "--lines", "25")
	}
	return interestingLines(out)
}

// interestingLines wirft weg, was in jeder Statusausgabe steht, und behält die
// Zeilen, die etwas über den Fehler sagen.
func interestingLines(out string) string {
	var keep []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
		case strings.HasPrefix(line, "Loaded:"),
			strings.HasPrefix(line, "Docs:"),
			strings.HasPrefix(line, "Process:"),
			strings.HasPrefix(line, "Main PID:"),
			strings.HasPrefix(line, "CPU:"),
			strings.HasPrefix(line, "Memory:"),
			strings.HasPrefix(line, "TasksMax:"),
			strings.HasPrefix(line, "Tasks:"),
			strings.HasPrefix(line, "CGroup:"),
			strings.HasPrefix(line, "●"),
			strings.Contains(line, "Consumed"),
			strings.Contains(line, "Scheduled restart job"),
			strings.Contains(line, "Stopped "),
			strings.Contains(line, "Starting "):
		default:
			keep = append(keep, line)
		}
	}
	if len(keep) == 0 {
		return ""
	}
	// Die letzten Zeilen: der Abbruch steht am Ende, nicht am Anfang.
	if len(keep) > 8 {
		keep = keep[len(keep)-8:]
	}
	return strings.Join(keep, " · ")
}

// startService startet einen Dienst und nennt bei einem Fehlschlag den Grund.
func (s *Server) startService(ctx context.Context, op Op, unit, action string) error {
	out, err := run(ctx, longTimeout, "systemctl", action, unit)
	if err == nil {
		return nil
	}
	msg := truncate(strings.TrimSpace(out), 200)
	if reason := s.serviceFailure(ctx, unit); reason != "" {
		msg += " — " + reason
	}
	return opErr(op, "%s %s: %s", unit, action, msg)
}
