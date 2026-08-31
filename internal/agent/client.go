package agent

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Client ist das Web-seitige Ende der Socket-Verbindung.
//
// Die Verbindung trägt genau eine Anfrage zur Zeit, deshalb serialisiert ein
// Mutex alle Aufrufe. Das genügt: Agent-Operationen sind selten und kurz,
// verglichen mit dem HTTP-Verkehr davor.
type Client struct {
	socketPath string
	timeout    time.Duration

	mu     sync.Mutex
	conn   net.Conn
	reader *bufio.Reader
	writer *bufio.Writer

	seq atomic.Uint64
}

func NewClient(socketPath string) *Client {
	return &Client{socketPath: socketPath, timeout: 150 * time.Second}
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeLocked()
}

func (c *Client) closeLocked() error {
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn, c.reader, c.writer = nil, nil, nil
	return err
}

// Call schickt eine Operation an den Agent und schreibt das Ergebnis nach out.
//
// Bei einem Verbindungsfehler wird genau einmal neu verbunden und wiederholt —
// ein Agent-Neustart (etwa durch `volt update`) soll nicht jede laufende
// Anfrage im Panel scheitern lassen. Wiederholt wird nur der Transportfehler;
// eine vom Agent abgelehnte Operation kommt unverändert zurück.
func (c *Client) Call(ctx context.Context, op Op, params any, out any) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	resp, err := c.callLocked(ctx, op, params)
	if err != nil && isTransportError(err) {
		_ = c.closeLocked()
		resp, err = c.callLocked(ctx, op, params)
	}
	if err != nil {
		return err
	}
	if !resp.OK {
		return &OpError{Op: op, Message: resp.Error}
	}
	if out == nil || len(resp.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(resp.Result, out); err != nil {
		return fmt.Errorf("%s: ergebnis unlesbar: %w", op, err)
	}
	return nil
}

func (c *Client) callLocked(ctx context.Context, op Op, params any) (*Response, error) {
	if err := c.connectLocked(); err != nil {
		return nil, err
	}

	req := Request{ID: strconv.FormatUint(c.seq.Add(1), 10), Op: op}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("%s: parameter nicht serialisierbar: %w", op, err)
		}
		req.Params = raw
	}
	if actor, ok := ctx.Value(actorKey{}).(string); ok {
		req.Actor = actor
	}

	deadline := time.Now().Add(c.timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := c.conn.SetDeadline(deadline); err != nil {
		return nil, transportErr{err}
	}

	if err := writeJSON(c.writer, req); err != nil {
		return nil, transportErr{fmt.Errorf("anfrage senden: %w", err)}
	}

	line, err := c.reader.ReadBytes('\n')
	if err != nil {
		return nil, transportErr{fmt.Errorf("antwort lesen: %w", err)}
	}

	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, transportErr{fmt.Errorf("antwort unlesbar: %w", err)}
	}
	if resp.ID != req.ID {
		// Die Antworten sind aus dem Tritt — die Verbindung ist nicht mehr
		// vertrauenswürdig und wird verworfen.
		return nil, transportErr{fmt.Errorf("antwort-id %q passt nicht zu anfrage %q", resp.ID, req.ID)}
	}
	return &resp, nil
}

func (c *Client) connectLocked() error {
	if c.conn != nil {
		return nil
	}

	conn, err := net.DialTimeout("unix", c.socketPath, 5*time.Second)
	if err != nil {
		return &UnavailableError{Socket: c.socketPath, Err: err}
	}
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		conn.Close()
		return err
	}

	reader, writer := bufio.NewReaderSize(conn, 64<<10), bufio.NewWriter(conn)
	if err := writeJSON(writer, Hello{Protocol: ProtocolVersion, Client: "volt-web"}); err != nil {
		conn.Close()
		return fmt.Errorf("handshake senden: %w", err)
	}

	line, err := reader.ReadBytes('\n')
	if err != nil {
		conn.Close()
		return fmt.Errorf("handshake-antwort: %w", err)
	}
	var ack HelloAck
	if err := json.Unmarshal(line, &ack); err != nil {
		conn.Close()
		return fmt.Errorf("handshake-antwort unlesbar: %w", err)
	}
	if !ack.OK {
		conn.Close()
		return fmt.Errorf("agent lehnt verbindung ab: %s", ack.Error)
	}

	c.conn, c.reader, c.writer = conn, reader, writer
	return nil
}

// Healthy sagt, ob der Agent gerade antwortet — für `volt doctor` und /healthz.
func (c *Client) Healthy(ctx context.Context) error {
	var res TextResult
	return c.Call(ctx, OpPing, nil, &res)
}

// --- Typisierte Hilfsaufrufe ----------------------------------------------

func (c *Client) SystemInfo(ctx context.Context) (*SystemInfo, error) {
	var info SystemInfo
	return &info, c.Call(ctx, OpSystemInfo, nil, &info)
}

func (c *Client) Services(ctx context.Context) ([]ServiceStatus, error) {
	var out []ServiceStatus
	return out, c.Call(ctx, OpServiceList, nil, &out)
}

func (c *Client) ServiceStatus(ctx context.Context, name string) (*ServiceStatus, error) {
	var st ServiceStatus
	return &st, c.Call(ctx, OpServiceStatus, ServiceParams{Name: name}, &st)
}

// ServiceAction führt start|stop|restart|reload|enable|disable aus.
func (c *Client) ServiceAction(ctx context.Context, action, name string) (*ServiceStatus, error) {
	op := Op("service." + action)
	switch op {
	case OpServiceStart, OpServiceStop, OpServiceRestart, OpServiceReload, OpServiceEnable, OpServiceDisable:
	default:
		return nil, fmt.Errorf("unbekannte dienst-aktion %q", action)
	}
	var st ServiceStatus
	return &st, c.Call(ctx, op, ServiceParams{Name: name}, &st)
}

func (c *Client) WriteVhost(ctx context.Context, domain, content string) error {
	return c.Call(ctx, OpNginxWriteVhost, VhostParams{Domain: domain, Content: content}, nil)
}

func (c *Client) RemoveVhost(ctx context.Context, domain string) error {
	return c.Call(ctx, OpNginxRemoveVhost, VhostParams{Domain: domain}, nil)
}

func (c *Client) WritePHPPool(ctx context.Context, phpVersion, poolName, content string) error {
	return c.Call(ctx, OpPHPWritePool,
		PoolParams{PHPVersion: phpVersion, PoolName: poolName, Content: content}, nil)
}

func (c *Client) RemovePHPPool(ctx context.Context, phpVersion, poolName string) error {
	return c.Call(ctx, OpPHPRemovePool, PoolParams{PHPVersion: phpVersion, PoolName: poolName}, nil)
}

func (c *Client) PHPVersions(ctx context.Context) ([]string, error) {
	var out []string
	return out, c.Call(ctx, OpPHPVersions, nil, &out)
}

func (c *Client) CreateSystemUser(ctx context.Context, username, homeDir string) error {
	return c.Call(ctx, OpUserCreate, UserCreateParams{Username: username, HomeDir: homeDir}, nil)
}

func (c *Client) DeleteSystemUser(ctx context.Context, username string, removeHome bool) error {
	return c.Call(ctx, OpUserDelete, UserDeleteParams{Username: username, RemoveHome: removeHome}, nil)
}

func (c *Client) Mkdir(ctx context.Context, path string, mode uint32, owner string) error {
	return c.Call(ctx, OpFileMkdir, FileMkdirParams{Path: path, Mode: mode, Owner: owner}, nil)
}

func (c *Client) WriteFile(ctx context.Context, path, content string, mode uint32, owner string) error {
	return c.Call(ctx, OpFileWrite,
		FileWriteParams{Path: path, Content: content, Mode: mode, Owner: owner}, nil)
}

func (c *Client) ReadFile(ctx context.Context, path string) (string, error) {
	var res TextResult
	err := c.Call(ctx, OpFileRead, FilePathParams{Path: path}, &res)
	return res.Text, err
}

func (c *Client) RemovePath(ctx context.Context, path string, recursive bool) error {
	return c.Call(ctx, OpFileRemove, FilePathParams{Path: path, Recursive: recursive}, nil)
}

func (c *Client) ListDir(ctx context.Context, path string) ([]FileEntry, error) {
	var out []FileEntry
	return out, c.Call(ctx, OpFileList, FilePathParams{Path: path}, &out)
}

func (c *Client) TailLog(ctx context.Context, path string, lines int) (string, error) {
	var res TextResult
	err := c.Call(ctx, OpFileTailLog, TailParams{Path: path, Lines: lines}, &res)
	return res.Text, err
}

func (c *Client) InstallCert(ctx context.Context, domain, certPEM, keyPEM string) (certPath, keyPath string, err error) {
	var res map[string]string
	err = c.Call(ctx, OpCertInstall,
		CertInstallParams{Domain: domain, CertPEM: certPEM, KeyPEM: keyPEM}, &res)
	return res["cert_path"], res["key_path"], err
}

// --- Actor-Weitergabe ------------------------------------------------------

type actorKey struct{}

// WithActor hängt den auslösenden Panel-User an den Context, damit der Agent
// ihn protokollieren kann.
func WithActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, actorKey{}, actor)
}

// UnavailableError heißt: der Agent läuft nicht. Das ist ein Zustand des
// Servers, kein Fehler des Aufrufers — die API muss daraus 503 machen und
// nicht 400, sonst sucht der Betreiber den Fehler in seiner Eingabe.
type UnavailableError struct {
	Socket string
	Err    error
}

func (e *UnavailableError) Error() string {
	return fmt.Sprintf("agent unter %s nicht erreichbar: %v", e.Socket, e.Err)
}

func (e *UnavailableError) Unwrap() error { return e.Err }

// transportErr markiert Fehler der Verbindung selbst — nur die werden wiederholt.
type transportErr struct{ err error }

func (e transportErr) Error() string { return e.err.Error() }
func (e transportErr) Unwrap() error { return e.err }

func isTransportError(err error) bool {
	var te transportErr
	return errors.As(err, &te)
}

// --- Dateien: erweiterte Operationen --------------------------------------

func (c *Client) MovePath(ctx context.Context, from, to string, overwrite bool) error {
	return c.Call(ctx, OpFileMove, FileMoveParams{From: from, To: to, Overwrite: overwrite}, nil)
}

func (c *Client) CopyPath(ctx context.Context, from, to string, overwrite bool) error {
	return c.Call(ctx, OpFileCopy, FileMoveParams{From: from, To: to, Overwrite: overwrite}, nil)
}

func (c *Client) Chmod(ctx context.Context, path string, mode uint32, recursive bool) error {
	return c.Call(ctx, OpFileChmod, FileChmodParams{Path: path, Mode: mode, Recursive: recursive}, nil)
}

func (c *Client) Stat(ctx context.Context, path string) (*StatResult, error) {
	var res StatResult
	return &res, c.Call(ctx, OpFileStat, FilePathParams{Path: path}, &res)
}

func (c *Client) Archive(ctx context.Context, sources []string, dest, owner string) (int64, error) {
	var res struct {
		SizeBytes int64 `json:"size_bytes"`
	}
	err := c.Call(ctx, OpFileArchive, ArchiveParams{Sources: sources, Dest: dest, Owner: owner}, &res)
	return res.SizeBytes, err
}

func (c *Client) Extract(ctx context.Context, archive, dest, owner string) (int, error) {
	var res struct {
		Entries int `json:"entries"`
	}
	err := c.Call(ctx, OpFileExtract, ExtractParams{Archive: archive, Dest: dest, Owner: owner}, &res)
	return res.Entries, err
}

// --- Datenbanken -----------------------------------------------------------

func (c *Client) CreateDatabase(ctx context.Context, name, charset, collation string) error {
	return c.Call(ctx, OpMySQLCreateDB,
		MySQLDBParams{Name: name, Charset: charset, Collation: collation}, nil)
}

func (c *Client) DropDatabase(ctx context.Context, name string) error {
	return c.Call(ctx, OpMySQLDropDB, MySQLDBParams{Name: name}, nil)
}

func (c *Client) CreateDBUser(ctx context.Context, p MySQLUserParams) error {
	return c.Call(ctx, OpMySQLCreateUser, p, nil)
}

func (c *Client) DropDBUser(ctx context.Context, username, host string) error {
	return c.Call(ctx, OpMySQLDropUser,
		MySQLUserParams{Username: username, HostPattern: host}, nil)
}

func (c *Client) GrantDBUser(ctx context.Context, p MySQLUserParams) error {
	return c.Call(ctx, OpMySQLGrant, p, nil)
}

func (c *Client) SetDBUserPassword(ctx context.Context, username, host, password string) error {
	return c.Call(ctx, OpMySQLSetPassword,
		MySQLUserParams{Username: username, HostPattern: host, Password: password}, nil)
}

// DatabaseSizes liefert die Belegung je Datenbank in Bytes.
func (c *Client) DatabaseSizes(ctx context.Context) (map[string]int64, error) {
	var out map[string]int64
	return out, c.Call(ctx, OpMySQLSizes, nil, &out)
}

func (c *Client) DumpDatabase(ctx context.Context, database, path string) (int64, error) {
	var res struct {
		SizeBytes int64 `json:"size_bytes"`
	}
	err := c.Call(ctx, OpMySQLDump, MySQLDumpParams{Database: database, Path: path}, &res)
	return res.SizeBytes, err
}

func (c *Client) ImportDatabase(ctx context.Context, database, path string) error {
	return c.Call(ctx, OpMySQLImport, MySQLDumpParams{Database: database, Path: path}, nil)
}

// --- Cronjobs --------------------------------------------------------------

func (c *Client) WriteCronjob(ctx context.Context, name, schedule, command, runAs string) error {
	return c.Call(ctx, OpCronWrite,
		CronParams{Name: name, Schedule: schedule, Command: command, RunAs: runAs}, nil)
}

func (c *Client) RemoveCronjob(ctx context.Context, name string) error {
	return c.Call(ctx, OpCronRemove, CronParams{Name: name}, nil)
}

func (c *Client) CronLog(ctx context.Context, name string, lines int) (string, error) {
	var res TextResult
	err := c.Call(ctx, OpCronLog, CronParams{Name: name, Lines: lines}, &res)
	return res.Text, err
}

// ReadChunk liest einen Block aus einer Datei. Der Aufrufer erhöht den Versatz,
// bis EOF gemeldet wird.
func (c *Client) ReadChunk(ctx context.Context, path string, offset int64, length int) (*ChunkResult, error) {
	var res ChunkResult
	err := c.Call(ctx, OpFileReadChunk,
		ChunkParams{Path: path, Offset: offset, Length: length}, &res)
	return &res, err
}

// WriteChunk schreibt einen Block. Beim ersten Block truncate=true setzen.
func (c *Client) WriteChunk(ctx context.Context, path string, offset int64, data []byte, owner string, truncate bool) error {
	return c.Call(ctx, OpFileWriteChunk, ChunkParams{
		Path: path, Offset: offset, Data: base64.StdEncoding.EncodeToString(data),
		Owner: owner, Truncate: truncate,
	}, nil)
}

// ChunkSize ist die Blockgröße, mit der Aufrufer arbeiten sollten.
const ChunkSize = maxChunkBytes
