//go:build darwin

package agent

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// checkPeer auf macOS über LOCAL_PEERCRED. Nur für die Entwicklung relevant —
// Zielsystem ist Linux.
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

	var cred *unix.Xucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	}); err != nil {
		return fmt.Errorf("peer-credentials: %w", err)
	}
	if credErr != nil {
		return fmt.Errorf("peer-credentials: %w", credErr)
	}

	if cred.Uid != 0 && int(cred.Uid) != s.peerUID {
		return fmt.Errorf("uid %d darf den agent nicht ansprechen (erwartet %d oder 0)", cred.Uid, s.peerUID)
	}
	return nil
}
