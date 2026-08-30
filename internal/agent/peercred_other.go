//go:build !linux && !darwin

package agent

import (
	"errors"
	"net"
)

// Auf Plattformen ohne Peer-Credentials verweigert der Agent den Dienst, statt
// die Prüfung stillschweigend zu überspringen.
func (s *Server) checkPeer(net.Conn) error {
	if s.peerUID < 0 {
		return nil
	}
	return errors.New("peer-credentials werden auf dieser plattform nicht unterstützt")
}
