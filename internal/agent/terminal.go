package agent

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// maxTerminals begrenzt, wie viele Shells gleichzeitig laufen können. Jede
	// belegt einen Prozess und ein Pseudoterminal.
	maxTerminals = 8
	// terminalIdle beendet eine Sitzung, in der nichts mehr passiert. Ein
	// vergessener Browser-Tab soll keine Shell offen halten.
	terminalIdle = 30 * time.Minute
	// terminalPickup ist die Frist, in der der Web-Prozess die Sitzung abholen
	// muss. Holt er sie nicht ab, war die Anfrage umsonst und die Shell wird
	// wieder beendet — sonst bliebe sie unerreichbar laufen.
	terminalPickup = 15 * time.Second
)

// terminal ist eine laufende Shell samt ihrem Pseudoterminal und dem Socket,
// über den der Web-Prozess die Bytes durchreicht.
type terminal struct {
	id     string
	user   string
	ptmx   *os.File
	cmd    *exec.Cmd
	socket string
	ln     net.Listener

	mu     sync.Mutex
	closed bool
}

// opTerminalOpen startet eine Shell als Systembenutzer einer Site.
//
// Der Benutzer kommt aus der Site, die der Web-Prozess im Namen des
// angemeldeten Kontos aufgelöst hat — nie aus dem Browser. Hier wird trotzdem
// noch einmal geprüft, dass es ein Site-Benutzer ist: der Agent verlässt sich
// nicht darauf, dass der Aufrufer nichts Falsches schickt.
func (s *Server) opTerminalOpen(_ context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[TerminalParams](raw, OpTerminalOpen)
	if err != nil {
		return nil, err
	}
	if err := checkUsername(p.User); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(p.User, sitePrefix) {
		return nil, opInputErr(OpTerminalOpen,
			"eine shell gibt es nur als systembenutzer einer site, nicht als %q", p.User)
	}
	dir, err := jail(p.Dir, s.roots)
	if err != nil {
		return nil, err
	}
	cols, rows := clampWindow(p.Cols, p.Rows)

	// Zähler-Prüfung und Platzreservierung atomar unter derselben Sperre: sonst
	// könnten mehrere gleichzeitige Anfragen die Prüfung alle bestehen, bevor
	// auch nur eine ihren Eintrag gesetzt hat, und gemeinsam mehr als
	// maxTerminals Sitzungen anlegen.
	s.termMu.Lock()
	if len(s.terms)+s.termReserved >= maxTerminals {
		s.termMu.Unlock()
		return nil, opInputErr(OpTerminalOpen,
			"es laufen bereits %d sitzungen — bitte zuerst eine schließen", maxTerminals)
	}
	s.termReserved++
	s.termMu.Unlock()

	// Ab hier reserviert, bis der Eintrag entweder in terms landet (ok = true)
	// oder die Reservierung bei einem Fehlschlag wieder freigegeben wird.
	ok := false
	defer func() {
		if !ok {
			s.termMu.Lock()
			s.termReserved--
			s.termMu.Unlock()
		}
	}()

	id, err := randomHex(12)
	if err != nil {
		return nil, opErr(OpTerminalOpen, "zufall: %v", err)
	}

	ptmx, slavePath, err := openPTY()
	if err != nil {
		return nil, opErr(OpTerminalOpen, "%v", err)
	}
	slave, err := os.OpenFile(slavePath, os.O_RDWR, 0)
	if err != nil {
		ptmx.Close()
		return nil, opErr(OpTerminalOpen, "terminal öffnen: %v", err)
	}
	defer slave.Close()

	if err := setWinsize(ptmx, cols, rows); err != nil {
		ptmx.Close()
		return nil, opErr(OpTerminalOpen, "fenstergröße: %v", err)
	}

	cmd, err := startShell(p.User, dir, slavePath, slave)
	if err != nil {
		ptmx.Close()
		return nil, opErr(OpTerminalOpen, "%v", err)
	}

	term := &terminal{id: id, user: p.User, ptmx: ptmx, cmd: cmd,
		socket: filepath.Join(filepath.Dir(s.socketPath), "term-"+id+".sock")}

	if err := s.listenTerminal(term); err != nil {
		term.close()
		return nil, opErr(OpTerminalOpen, "%v", err)
	}

	s.termMu.Lock()
	s.terms[id] = term
	s.termReserved--
	s.termMu.Unlock()
	ok = true

	s.log.Info("terminal eröffnet", "sitzung", id, "benutzer", p.User, "pid", cmd.Process.Pid)
	go s.serveTerminal(term)

	return TerminalSession{Session: id, Socket: term.socket, User: p.User}, nil
}

// listenTerminal öffnet den Socket dieser Sitzung mit denselben Rechten wie den
// des Agents: nur der Web-Benutzer darf sich verbinden.
func (s *Server) listenTerminal(term *terminal) error {
	_ = os.Remove(term.socket)
	ln, err := net.Listen("unix", term.socket)
	if err != nil {
		return err
	}
	if err := os.Chmod(term.socket, 0o660); err != nil {
		ln.Close()
		return err
	}
	if s.peerGID >= 0 {
		if err := os.Chown(term.socket, -1, s.peerGID); err != nil {
			ln.Close()
			return err
		}
	}
	term.ln = ln
	return nil
}

// serveTerminal nimmt genau eine Verbindung an und schaufelt danach Bytes.
//
// Genau eine: ein zweiter Verbinder würde am selben Terminal mitschreiben und
// mitlesen. Nach dem Verbindungsaufbau verschwindet der Socket.
func (s *Server) serveTerminal(term *terminal) {
	defer s.dropTerminal(term)

	if ul, ok := term.ln.(*net.UnixListener); ok {
		_ = ul.SetDeadline(time.Now().Add(terminalPickup))
	}
	conn, err := term.ln.Accept()
	term.ln.Close()
	_ = os.Remove(term.socket)
	if err != nil {
		s.log.Warn("terminal wurde nicht abgeholt", "sitzung", term.id, "err", err)
		return
	}
	defer conn.Close()

	// Beide Richtungen laufen parallel; endet eine, endet die Sitzung.
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(term.ptmx, idleReader{conn: conn})
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(conn, term.ptmx)
		done <- struct{}{}
	}()
	<-done
}

// idleReader beendet die Sitzung, wenn lange nichts mehr getippt wurde. Der
// Zeitgeber sitzt bewusst auf der Eingabe: ein Programm, das ununterbrochen
// ausgibt, hielte die Sitzung sonst endlos offen.
type idleReader struct{ conn net.Conn }

func (r idleReader) Read(p []byte) (int, error) {
	if err := r.conn.SetReadDeadline(time.Now().Add(terminalIdle)); err != nil {
		return 0, err
	}
	return r.conn.Read(p)
}

func (s *Server) opTerminalResize(_ context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[TerminalParams](raw, OpTerminalResize)
	if err != nil {
		return nil, err
	}
	term := s.terminal(p.Session)
	if term == nil {
		return nil, opInputErr(OpTerminalResize, "sitzung %q gibt es nicht", p.Session)
	}
	cols, rows := clampWindow(p.Cols, p.Rows)

	// term.mu sperren, bevor auf ptmx zugegriffen wird — analog zu
	// terminal.close(), das genau dieses ptmx unter derselben Sperre schließt.
	// Liefen resize und close gleichzeitig, konnte setWinsize sonst einen
	// bereits ungültigen (oder wiederverwendeten) Dateideskriptor per ioctl
	// ansprechen.
	term.mu.Lock()
	defer term.mu.Unlock()
	if term.closed {
		return nil, opInputErr(OpTerminalResize, "sitzung %q gibt es nicht", p.Session)
	}
	if err := setWinsize(term.ptmx, cols, rows); err != nil {
		return nil, opErr(OpTerminalResize, "%v", err)
	}
	return TextResult{Text: "größe übernommen"}, nil
}

func (s *Server) opTerminalClose(_ context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[TerminalParams](raw, OpTerminalClose)
	if err != nil {
		return nil, err
	}
	term := s.terminal(p.Session)
	if term == nil {
		// Schon zu — das ist kein Fehler, sondern das Ziel.
		return TextResult{Text: "sitzung ist beendet"}, nil
	}
	s.dropTerminal(term)
	return TextResult{Text: "sitzung beendet"}, nil
}

func (s *Server) terminal(id string) *terminal {
	s.termMu.Lock()
	defer s.termMu.Unlock()
	return s.terms[id]
}

func (s *Server) dropTerminal(term *terminal) {
	s.termMu.Lock()
	_, known := s.terms[term.id]
	delete(s.terms, term.id)
	s.termMu.Unlock()

	term.close()
	if known {
		s.log.Info("terminal beendet", "sitzung", term.id, "benutzer", term.user)
	}
}

// closeTerminals räumt beim Herunterfahren des Agents auf. Sonst überlebten die
// Shells den Prozess, der sie gestartet hat.
func (s *Server) closeTerminals() {
	s.termMu.Lock()
	list := make([]*terminal, 0, len(s.terms))
	for _, t := range s.terms {
		list = append(list, t)
	}
	s.terms = map[string]*terminal{}
	s.termMu.Unlock()

	for _, t := range list {
		t.close()
	}
}

func clampWindow(cols, rows int) (int, int) {
	if cols < 20 || cols > 500 {
		cols = 80
	}
	if rows < 5 || rows > 200 {
		rows = 24
	}
	return cols, rows
}
