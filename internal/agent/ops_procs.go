package agent

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// maxProcesses deckelt die Antwort. Eine Liste mit tausenden Einträgen ist
// weder lesbar noch nützlich; interessant ist immer das obere Ende.
const maxProcesses = 300

// sitePrefix kennzeichnet die Systembenutzer der Sites. Nur deren Prozesse
// lassen sich über das Panel beenden.
const sitePrefix = "site_"

func (s *Server) opSystemProcesses(_ context.Context, _ json.RawMessage) (any, error) {
	procs, err := readProcesses()
	if err != nil {
		return nil, opErr(OpSystemProcesses, "%v", err)
	}

	sort.Slice(procs, func(i, j int) bool {
		if procs[i].CPUPercent != procs[j].CPUPercent {
			return procs[i].CPUPercent > procs[j].CPUPercent
		}
		return procs[i].MemBytes > procs[j].MemBytes
	})
	if len(procs) > maxProcesses {
		procs = procs[:maxProcesses]
	}
	return procs, nil
}

// opSystemProcessKill beendet einen Prozess — aber nur einen, der einer Site
// gehört.
//
// Zwei Schranken, die zusammengehören: der Web-Prozess sagt, welcher Benutzer
// erwartet wird (er kennt den Mandanten), und der Agent prüft, dass der Prozess
// diesem Benutzer wirklich gehört und dass es überhaupt ein Site-Benutzer ist.
// Ohne die zweite Prüfung wäre die Operation ein Weg, sshd oder den Agent
// selbst abzuschießen; ohne die erste könnte ein Kunde den Prozess eines
// anderen beenden.
func (s *Server) opSystemProcessKill(_ context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[ProcessKillParams](raw, OpSystemProcessKill)
	if err != nil {
		return nil, err
	}
	if p.PID <= 1 {
		return nil, opInputErr(OpSystemProcessKill, "pid %d ist nicht zulässig", p.PID)
	}
	if err := checkUsername(p.User); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(p.User, sitePrefix) {
		return nil, opInputErr(OpSystemProcessKill,
			"nur prozesse einer site lassen sich beenden, %q ist kein site-benutzer", p.User)
	}

	signal := syscall.SIGTERM
	switch p.Signal {
	case "", "TERM":
	case "KILL":
		signal = syscall.SIGKILL
	default:
		return nil, opInputErr(OpSystemProcessKill, "signal %q ist nicht vorgesehen", p.Signal)
	}

	owner, err := processOwner(p.PID)
	if err != nil {
		return nil, opInputErr(OpSystemProcessKill, "%v", err)
	}
	if owner != p.User {
		// Ohne Angabe, wem er wirklich gehört: das wäre eine Auskunft über
		// fremde Mandanten.
		return nil, opInputErr(OpSystemProcessKill,
			"prozess %d gehört nicht zu %s", p.PID, p.User)
	}

	if err := syscall.Kill(p.PID, signal); err != nil {
		return nil, opErr(OpSystemProcessKill, "%v", err)
	}
	return TextResult{Text: "prozess " + strconv.Itoa(p.PID) + " beendet"}, nil
}
