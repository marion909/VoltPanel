package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// maxLineBytes deckelt eine einzelne Anfrage. Ohne Deckel könnte der Web-Prozess
// den Root-Daemon per Riesen-Payload aus dem Speicher drängen.
const maxLineBytes = 8 << 20 // 8 MiB

// Handler führt eine Operation aus. Die Zuordnung Op -> Handler in newRegistry
// ist die vollständige Liste dessen, was der Agent kann.
type Handler func(ctx context.Context, raw json.RawMessage) (any, error)

type Server struct {
	socketPath string
	peerUID    int // erlaubte UID des Web-Prozesses, -1 = jede
	// peerGID ist die Hauptgruppe des Peers und nicht dasselbe wie peerUID:
	// useradd vergibt Benutzer- und Gruppennummern unabhängig voneinander,
	// sie stimmen nur häufig zufällig überein.
	peerGID  int
	log      *slog.Logger
	roots    []string // erlaubte Datei-Wurzeln
	nginxDir string
	phpDir   string
	certDir  string
	logDir   string
	// panelDomain kommt aus der Konfiguration, nicht aus der Anfrage: nur so
	// kann der Web-Prozess sich nicht selbst zum Eigentümer eines fremden
	// Schlüssels erklären.
	panelDomain string

	registry map[Op]Handler
	listener net.Listener

	mu   sync.Mutex
	conn int
}

type ServerOptions struct {
	SocketPath string
	PeerUser   string // Systemuser, der sich verbinden darf (üblicherweise "volt")
	Logger     *slog.Logger
	NginxDir   string
	PHPDir     string
	CertDir    string
	SitesDir   string
	LogDir     string
	// PanelDomain ist die Domain des Panels selbst. Ihr Schlüssel bekommt den
	// Peer als Eigentümer, weil volt-web ihn lesen muss.
	PanelDomain string
}

func NewServer(opts ServerOptions) (*Server, error) {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.SocketPath == "" {
		opts.SocketPath = "/run/volt/agent.sock"
	}
	setDefault(&opts.NginxDir, "/etc/nginx")
	setDefault(&opts.PHPDir, "/etc/php")
	setDefault(&opts.CertDir, "/var/lib/volt/certs")
	setDefault(&opts.SitesDir, "/var/www")
	setDefault(&opts.LogDir, "/var/log")

	peerUID, peerGID := -1, -1
	if opts.PeerUser != "" {
		u, err := user.Lookup(opts.PeerUser)
		if err != nil {
			return nil, fmt.Errorf("peer-user %q: %w", opts.PeerUser, err)
		}
		if peerUID, err = strconv.Atoi(u.Uid); err != nil {
			return nil, fmt.Errorf("peer-uid %q: %w", u.Uid, err)
		}
		if peerGID, err = strconv.Atoi(u.Gid); err != nil {
			return nil, fmt.Errorf("peer-gid %q: %w", u.Gid, err)
		}
	}

	s := &Server{
		socketPath:  opts.SocketPath,
		peerUID:     peerUID,
		peerGID:     peerGID,
		log:         opts.Logger,
		nginxDir:    opts.NginxDir,
		phpDir:      opts.PHPDir,
		certDir:     opts.CertDir,
		logDir:      opts.LogDir,
		panelDomain: opts.PanelDomain,
		// Alles, was der Agent an Dateien überhaupt anfassen darf.
		roots: []string{opts.SitesDir, opts.NginxDir, opts.PHPDir, opts.CertDir, opts.LogDir},
	}
	s.registry = s.newRegistry()
	return s, nil
}

// Listen öffnet den Unix-Socket mit 0660 root:<peer>, sodass ausschließlich der
// Web-User ihn ansprechen kann.
func (s *Server) Listen() error {
	if err := os.MkdirAll(filepath.Dir(s.socketPath), 0o755); err != nil {
		return fmt.Errorf("socket-verzeichnis: %w", err)
	}
	// Ein Socket aus einem abgestürzten Vorgänger würde Bind blockieren.
	if err := os.Remove(s.socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("alten socket entfernen: %w", err)
	}

	ln, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("socket %s: %w", s.socketPath, err)
	}
	if err := os.Chmod(s.socketPath, 0o660); err != nil {
		ln.Close()
		return fmt.Errorf("socket-rechte: %w", err)
	}
	if s.peerGID >= 0 {
		// Die Gruppe des Web-Users, damit 0660 überhaupt jemanden durchlässt.
		// -1 für den Eigentümer heißt "unverändert" — der Agent läuft als
		// root und ist bereits Eigentümer.
		//
		// Kein Warnen, sondern Abbruch: einen Socket, den der Panel-Prozess
		// nicht öffnen darf, macht den Agent nutzlos. Das soll beim Start
		// auffallen und nicht erst bei der ersten Operation.
		if err := os.Chown(s.socketPath, -1, s.peerGID); err != nil {
			ln.Close()
			return fmt.Errorf("socket-gruppe auf %d setzen: %w", s.peerGID, err)
		}
	}

	s.listener = ln
	s.log.Info("agent hört", "socket", s.socketPath, "peer_uid", s.peerUID, "peer_gid", s.peerGID)
	return nil
}

func (s *Server) Serve(ctx context.Context) error {
	if s.listener == nil {
		return errors.New("Listen() wurde nicht aufgerufen")
	}
	defer s.listener.Close()

	go func() {
		<-ctx.Done()
		s.listener.Close()
	}()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil // sauberes Herunterfahren
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			s.log.Warn("accept fehlgeschlagen", "err", err)
			continue
		}
		go s.handleConn(ctx, conn)
	}
}

func (s *Server) Close() error {
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	// Wer verbindet, wird über die Kernel-Credentials des Sockets geprüft —
	// nicht über einen Token, den ein übernommener Web-Prozess ohnehin hätte.
	if err := s.checkPeer(conn); err != nil {
		s.log.Warn("verbindung abgelehnt", "err", err)
		return
	}

	s.mu.Lock()
	s.conn++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.conn--
		s.mu.Unlock()
	}()

	reader := bufio.NewReaderSize(conn, 64<<10)
	writer := bufio.NewWriter(conn)

	if err := s.handshake(reader, writer); err != nil {
		s.log.Warn("handshake fehlgeschlagen", "err", err)
		return
	}

	dec := json.NewDecoder(io.LimitReader(reader, maxLineBytes))
	for {
		var req Request
		if err := dec.Decode(&req); err != nil {
			if !errors.Is(err, io.EOF) && ctx.Err() == nil {
				s.log.Debug("verbindung beendet", "err", err)
			}
			return
		}

		resp := s.dispatch(ctx, &req)
		if err := writeJSON(writer, resp); err != nil {
			s.log.Warn("antwort nicht zustellbar", "err", err)
			return
		}
		// Nach jeder Anfrage darf wieder die volle Größe gelesen werden.
		dec = json.NewDecoder(io.LimitReader(reader, maxLineBytes))
	}
}

func (s *Server) handshake(r *bufio.Reader, w *bufio.Writer) error {
	line, err := r.ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("hello lesen: %w", err)
	}
	var hello Hello
	if err := json.Unmarshal(line, &hello); err != nil {
		return fmt.Errorf("hello ungültig: %w", err)
	}

	ack := HelloAck{Protocol: ProtocolVersion, Agent: "volt-agent", OK: true}
	if hello.Protocol != ProtocolVersion {
		ack.OK = false
		ack.Error = fmt.Sprintf("protokoll v%d, agent spricht v%d — bitte beide dienste aktualisieren",
			hello.Protocol, ProtocolVersion)
	}
	if err := writeJSON(w, ack); err != nil {
		return err
	}
	if !ack.OK {
		return errors.New(ack.Error)
	}
	return nil
}

// dispatch schlägt die Operation in der Registry nach. Steht sie nicht drin,
// passiert nichts — das ist die eigentliche Whitelist.
func (s *Server) dispatch(ctx context.Context, req *Request) *Response {
	start := time.Now()
	resp := &Response{ID: req.ID}

	handler, ok := s.registry[req.Op]
	if !ok {
		resp.Error = fmt.Sprintf("unbekannte operation %q", req.Op)
		s.log.Warn("operation abgelehnt", "op", req.Op, "actor", req.Actor)
		return resp
	}

	result, err := handler(ctx, req.Params)
	if err != nil {
		resp.Error = err.Error()
		// errBadInput deckt die Prüfungen ab, die es schon gab
		// (checkDomain, checkUsername, checkPHPVersion …); OpError.Input die
		// neueren. Beide meinen dasselbe: der Aufrufer hat Unsinn geschickt.
		var opE *OpError
		resp.Input = errors.Is(err, errBadInput) || (errors.As(err, &opE) && opE.Input)
		s.log.Warn("operation fehlgeschlagen",
			"op", req.Op, "actor", req.Actor, "dauer", time.Since(start), "err", err)
		return resp
	}

	if result != nil {
		encoded, encErr := json.Marshal(result)
		if encErr != nil {
			resp.Error = fmt.Sprintf("ergebnis nicht serialisierbar: %v", encErr)
			return resp
		}
		resp.Result = encoded
	}
	resp.OK = true

	// Lesende Operationen fluten das Log sonst.
	if isMutating(req.Op) {
		s.log.Info("operation ausgeführt", "op", req.Op, "actor", req.Actor, "dauer", time.Since(start))
	}
	return resp
}

func isMutating(op Op) bool {
	switch op {
	case OpPing, OpSystemInfo, OpDiskUsage, OpServiceList, OpServiceStatus,
		OpNginxTest, OpPHPVersions, OpFileRead, OpFileList, OpFileTailLog, OpUserExists:
		return false
	}
	return true
}

func writeJSON(w *bufio.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := w.Write(append(b, '\n')); err != nil {
		return err
	}
	return w.Flush()
}

func setDefault(p *string, v string) {
	if *p == "" {
		*p = v
	}
}

// decode packt die Parameter aus und meldet einen brauchbaren Fehler statt eines
// nil-Dereferenzierens, wenn sie fehlen.
func decode[T any](raw json.RawMessage, op Op) (T, error) {
	var v T
	if len(raw) == 0 {
		return v, opErr(op, "parameter fehlen")
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, opErr(op, "parameter unlesbar: %v", err)
	}
	return v, nil
}
