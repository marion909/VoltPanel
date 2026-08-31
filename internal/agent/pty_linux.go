//go:build linux

package agent

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

// Hier steht die einzige Stelle im Agent, an der absichtlich eine Shell
// startet. Sie ist kein Widerspruch zu "nie sh -c": dort geht es darum, dass
// aus Text niemals ein Kommando wird. Hier ist die Shell das Ziel, sie
// bekommt kein Argument, und sie läuft nie als root — sondern als der
// unprivilegierte Systembenutzer einer Site.
//
// Damit gibt das Terminal nichts her, was nicht ohnehin ginge: ein Cronjob
// derselben Site führt beliebige Befehle unter demselben Konto aus. Was es
// NICHT gibt, ist eine Shell als root oder als ein fremder Benutzer.
const (
	shellPath     = "/bin/bash"
	fallbackShell = "/bin/sh"
)

// openPTY öffnet ein Pseudoterminal und gibt Master und Slave-Pfad zurück.
//
// Ohne Fremdpaket: /dev/ptmx öffnen, entsperren, Nummer erfragen. Das sind die
// drei Schritte, die posix_openpt, unlockpt und ptsname in der libc auch tun.
func openPTY() (*os.File, string, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, "", fmt.Errorf("/dev/ptmx: %w", err)
	}
	fd := int(master.Fd())
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		master.Close()
		return nil, "", fmt.Errorf("pty entsperren: %w", err)
	}
	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		master.Close()
		return nil, "", fmt.Errorf("pty-nummer: %w", err)
	}
	return master, "/dev/pts/" + strconv.Itoa(n), nil
}

func setWinsize(f *os.File, cols, rows int) error {
	return unix.IoctlSetWinsize(int(f.Fd()), unix.TIOCSWINSZ, &unix.Winsize{
		Row: uint16(rows), Col: uint16(cols),
	})
}

// startShell startet die Shell als username auf dem übergebenen Slave.
func startShell(username, dir, slavePath string, slave *os.File) (*exec.Cmd, error) {
	u, err := user.Lookup(username)
	if err != nil {
		return nil, fmt.Errorf("benutzer %q: %w", username, err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return nil, err
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return nil, err
	}
	if uid == 0 {
		// Sollte durch die Prüfung auf den site_-Präfix nie eintreten. Wenn
		// doch, ist hier Schluss: eine Root-Shell im Browser ist genau das,
		// was die Trennung zwischen Web und Agent verhindern soll.
		return nil, fmt.Errorf("eine shell als root gibt es hier nicht")
	}

	// Das Terminal gehört dem Benutzer, sonst scheitert alles, was /dev/tty
	// neu öffnet — sudo, ssh, vim, less.
	if err := os.Chown(slavePath, uid, gid); err != nil {
		return nil, fmt.Errorf("terminal übereignen: %w", err)
	}
	if err := os.Chmod(slavePath, 0o620); err != nil {
		return nil, fmt.Errorf("terminal-rechte: %w", err)
	}

	shell := shellPath
	if _, err := os.Stat(shell); err != nil {
		shell = fallbackShell
	}

	cmd := exec.Command(shell, "-i")
	cmd.Dir = dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	// Festes, knappes Environment — nichts wird vom Agent geerbt.
	cmd.Env = []string{
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"HOME=" + u.HomeDir,
		"USER=" + username,
		"LOGNAME=" + username,
		"SHELL=" + shell,
		"TERM=xterm-256color",
		"LANG=C.UTF-8",
		"PS1=\\u@\\h:\\w\\$ ",
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		// Eigene Sitzung mit dem Slave als steuerndem Terminal: nur so wirken
		// Strg-C und Strg-Z auf die Shell und nicht auf den Agent.
		Setsid:  true,
		Setctty: true,
		Ctty:    0,
		// Rechte fallen lassen, bevor das Programm startet. Groups leer:
		// keine Nebengruppen, auch nicht die des Agents.
		Credential: &syscall.Credential{
			Uid: uint32(uid), Gid: uint32(gid), Groups: []uint32{},
		},
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("shell starten: %w", err)
	}
	return cmd, nil
}
