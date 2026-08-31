package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// passwdLockFiles sind die Sperren, die useradd und Verwandte anlegen.
//
// /etc/.pwd.lock steht bewusst nicht dabei: die Datei existiert auf Debian
// dauerhaft und wird über fcntl gesperrt. Sie als Überbleibsel zu melden
// würde bei jedem Fehler in die falsche Richtung zeigen.
var passwdLockFiles = []string{
	"/etc/passwd.lock",
	"/etc/group.lock",
	"/etc/shadow.lock",
	"/etc/gshadow.lock",
	"/etc/subuid.lock",
	"/etc/subgid.lock",
}

// diagnoseUserLock erklärt ein "cannot lock /etc/passwd".
//
// Die Meldung von useradd nennt den Grund nicht, und die beiden möglichen
// Ursachen verlangen entgegengesetzte Schritte: ein schreibgeschütztes /etc
// braucht eine geänderte Unit, ein liegengebliebenes Sperrfile braucht ein rm.
// Ohne diese Unterscheidung rät man abwechselnd das eine und das andere.
func diagnoseUserLock() string {
	if hint := probeEtcWritable(); hint != "" {
		return hint
	}
	if hint := staleLockHint(); hint != "" {
		return hint
	}
	return "weder ist /etc schreibgeschützt noch liegt eine Sperrdatei herum — " +
		"möglicherweise hält gerade ein anderer Vorgang /etc/.pwd.lock"
}

// probeEtcWritable prüft, ob der Agent in /etc schreiben darf. Genau das
// verhindert ProtectSystem=full ohne passenden Eintrag in ReadWritePaths.
func probeEtcWritable() string {
	probe := filepath.Join("/etc", ".volt-write-probe")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err == nil {
		f.Close()
		os.Remove(probe)
		return ""
	}
	if errors.Is(err, os.ErrExist) {
		// Eine eigene Probe von vorhin — dann war /etc beschreibbar.
		os.Remove(probe)
		return ""
	}
	if errors.Is(err, syscall.EROFS) {
		return "/etc ist für den Agent schreibgeschützt. useradd legt seine Sperrdatei dort an. " +
			"Die Unit braucht /etc in ReadWritePaths — prüfen mit " +
			"`systemctl show volt-agent -p ReadWritePaths`, danach " +
			"`systemctl daemon-reload && systemctl restart volt-agent`"
	}
	return fmt.Sprintf("/etc ist nicht beschreibbar: %v", err)
}

// staleLockHint meldet liegengebliebene Sperrdateien. Sie entstehen, wenn ein
// useradd abgebrochen wird, und blockieren danach jeden weiteren Versuch —
// dauerhaft, nicht "try again later".
func staleLockHint() string {
	var found []string
	for _, path := range passwdLockFiles {
		info, err := os.Lstat(path)
		if err != nil {
			continue
		}
		found = append(found, fmt.Sprintf("%s (liegt seit %s)",
			path, time.Since(info.ModTime()).Round(time.Second)))
	}
	if len(found) == 0 {
		return ""
	}
	return "es liegen Sperrdateien aus einem abgebrochenen Vorgang herum: " +
		strings.Join(found, ", ") +
		" — nach dem Prüfen entfernen: `rm -f " + strings.Join(passwdLockFiles, " ") + "`"
}
