//go:build linux

package agent

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// checkPeer prüft über SO_PEERCRED, welcher Systemuser sich verbunden hat.
//
// Der Kernel liefert diese Angabe, nicht der Client — sie ist damit nicht
// fälschbar. Ein Token im Protokoll wäre schwächer: Wer den Socket lesen darf,
// könnte auch die Token-Datei lesen.
func (s *Server) checkPeer(conn net.Conn) error {
	if s.peerUID < 0 {
		return nil
	}
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("verbindung ist kein unix-socket")
	}

	raw, err := uc.SyscallConn()
	if err != nil {
		return fmt.Errorf("socket-handle: %w", err)
	}

	var cred *unix.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return fmt.Errorf("peer-credentials: %w", err)
	}
	if credErr != nil {
		return fmt.Errorf("peer-credentials: %w", credErr)
	}

	// root darf immer (CLI läuft als root), sonst nur der konfigurierte Web-User.
	if cred.Uid != 0 && int(cred.Uid) != s.peerUID {
		return fmt.Errorf("uid %d darf den agent nicht ansprechen (erwartet %d oder 0)", cred.Uid, s.peerUID)
	}
	return nil
}
