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
// Jede Verbindung trägt genau eine Anfrage zur Zeit, deshalb serialisiert ein
// Mutex ihre Aufrufe. Es gibt zwei Verbindungen, nicht eine: "fast" für die
// gewöhnlichen, kurzen Aufrufe (Statusabfragen, Dateizugriffe, Terminal) und
// "slow" für die wenigen Operationen, die mehrminütige Zeitlimits eintragen
// (Systemupdate, Paket-/WordPress-/Webmail-Installation). Über eine einzige
// gemeinsame Verbindung blockierte eine laufende Installation sonst jeden
// anderen Agent-Aufruf im gesamten Panel für ihre volle Dauer — dem
// widerspricht der Kommentar, den dieser Absatz ersetzt: "Agent-Operationen
// sind selten und kurz" trifft auf die lang laufenden gerade nicht zu.
type Client struct {
	socketPath string
	timeout    time.Duration

	fast agentConn
	slow agentConn

	seq atomic.Uint64
}

// agentConn ist eine einzelne Socket-Verbindung samt der Sperre, die ihre
// Aufrufe serialisiert.
type agentConn struct {
	mu     sync.Mutex
	conn   net.Conn
	reader *bufio.Reader
	writer *bufio.Writer
}

func NewClient(socketPath string) *Client {
	return &Client{socketPath: socketPath, timeout: 150 * time.Second}
}

func (c *Client) Close() error {
	err1 := c.fast.close()
	err2 := c.slow.close()
	if err1 != nil {
		return err1
	}
	return err2
}

func (ac *agentConn) close() error {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	return ac.closeLocked()
}

func (ac *agentConn) closeLocked() error {
	if ac.conn == nil {
		return nil
	}
	err := ac.conn.Close()
	ac.conn, ac.reader, ac.writer = nil, nil, nil
	return err
}

// Call schickt eine Operation über die "fast"-Verbindung an den Agent und
// schreibt das Ergebnis nach out. Für die wenigen Operationen mit
// mehrminütigem Zeitlimit siehe callSlow.
//
// Bei einem Verbindungsfehler wird genau einmal neu verbunden und wiederholt —
// ein Agent-Neustart (etwa durch `volt update`) soll nicht jede laufende
// Anfrage im Panel scheitern lassen. Wiederholt wird nur der Transportfehler;
// eine vom Agent abgelehnte Operation kommt unverändert zurück.
func (c *Client) Call(ctx context.Context, op Op, params any, out any) error {
	// Ein nicht vorhandener Agent ist ein Zustand, keine Katastrophe: im Test
	// steht dort bewusst nil, und im laufenden Panel liefe ein Aufruf ohne
	// Verbindung sonst in einen Nil-Zugriff — der nähme in einer Goroutine den
	// ganzen Prozess mit. Eine Fehlermeldung sagt dasselbe und lässt das Panel
	// stehen.
	if c == nil {
		return fmt.Errorf("%s: kein agent verbunden", op)
	}
	return c.call(ctx, &c.fast, op, params, out)
}

// callSlow ist Call über die zweite, eigene Verbindung — für Operationen, die
// selbst mehrminütige Zeitlimits eintragen (Systemupdate, Paket-/WordPress-/
// Webmail-Installation). Über dieselbe Verbindung wie die kurzen, häufigen
// Aufrufe blockierte eine laufende Installation sonst jeden anderen
// Agent-Aufruf im gesamten Panel für ihre volle Dauer.
func (c *Client) callSlow(ctx context.Context, op Op, params any, out any) error {
	if c == nil {
		return fmt.Errorf("%s: kein agent verbunden", op)
	}
	return c.call(ctx, &c.slow, op, params, out)
}

func (c *Client) call(ctx context.Context, ac *agentConn, op Op, params any, out any) error {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	resp, err := c.callLocked(ctx, ac, op, params)
	if err != nil && isTransportError(err) {
		_ = ac.closeLocked()
		resp, err = c.callLocked(ctx, ac, op, params)
	}
	if err != nil {
		return err
	}
	if !resp.OK {
		return &OpError{Op: op, Message: resp.Error, Input: resp.Input}
	}
	if out == nil || len(resp.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(resp.Result, out); err != nil {
		return fmt.Errorf("%s: ergebnis unlesbar: %w", op, err)
	}
	return nil
}

func (c *Client) callLocked(ctx context.Context, ac *agentConn, op Op, params any) (*Response, error) {
	if err := c.connectLocked(ac); err != nil {
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

	// Ein ausdrücklich gesetztes Zeitlimit gilt, in beide Richtungen. Der
	// Vorgabewert deckelt gewöhnliche Operationen; ein Update dauert länger
	// als alles andere und sagt das über seinen Context.
	deadline := time.Now().Add(c.timeout)
	if d, ok := ctx.Deadline(); ok {
		deadline = d
	}
	if err := ac.conn.SetDeadline(deadline); err != nil {
		return nil, transportErr{err}
	}

	if err := writeJSON(ac.writer, req); err != nil {
		return nil, transportErr{fmt.Errorf("anfrage senden: %w", err)}
	}

	line, err := ac.reader.ReadBytes('\n')
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

func (c *Client) connectLocked(ac *agentConn) error {
	if ac.conn != nil {
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

	ac.conn, ac.reader, ac.writer = conn, reader, writer
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

// DiskUsage misst den Platzverbrauch eines Verzeichnisses.
func (c *Client) DiskUsage(ctx context.Context, path string) (*DiskUsageResult, error) {
	var res DiskUsageResult
	return &res, c.Call(ctx, OpDiskUsage, DiskUsageParams{Path: path}, &res)
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

// PHPExtensions listet die Module einer PHP-Version.
func (c *Client) PHPExtensions(ctx context.Context, version string) ([]PHPExtension, error) {
	var out []PHPExtension
	err := c.Call(ctx, OpPHPExtensions, PoolParams{PHPVersion: version}, &out)
	return out, err
}

// InstallPHPExtension holt ein Modul nach. Der Paketname entsteht im Agent
// aus Version und Modulname — hier geht keiner über die Leitung.
func (c *Client) InstallPHPExtension(ctx context.Context, version, name string) error {
	// Paketinstallationen dauern länger als die Vorgabe des Clients.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	return c.callSlow(ctx, OpPHPExtInstall, PHPExtParams{PHPVersion: version, Name: name}, nil)
}

// TogglePHPExtension schaltet ein installiertes Modul an oder ab.
func (c *Client) TogglePHPExtension(ctx context.Context, version, name string, enable bool) error {
	return c.Call(ctx, OpPHPExtToggle,
		PHPExtParams{PHPVersion: version, Name: name, Enable: enable}, nil)
}

// SystemUpdate stößt das Update an. Ohne Parameter: welche Version kommt,
// entscheidet der Kanal in der Konfiguration des Agents, nicht der Aufrufer.
func (c *Client) SystemUpdate(ctx context.Context) (UpdateResult, error) {
	// Download beider Binaries plus Migration. Das Zeitlimit setzt der
	// Aufrufer nicht selbst: es gehört zur Operation, nicht zum Aufruf.
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	var res UpdateResult
	err := c.callSlow(ctx, OpSystemUpdate, nil, &res)
	return res, err
}

// WriteShared legt die vhost-übergreifende Config ab. Ohne sie beantwortet
// nginx Anfragen an unbekannte Hostnamen mit der Standardseite der
// Distribution — und die ACME-Prüfung läuft ins Leere.
func (c *Client) WriteShared(ctx context.Context, content string) error {
	return c.Call(ctx, OpNginxWriteShared, SharedParams{Content: content}, nil)
}

func (c *Client) RemoveVhost(ctx context.Context, domain string) error {
	return c.Call(ctx, OpNginxRemoveVhost, VhostParams{Domain: domain}, nil)
}

// WriteHtpasswd legt die Datei für den Passwortschutz an. Die Einträge sind
// bereits gehasht — Klartextpasswörter gehen nie über den Socket.
func (c *Client) WriteHtpasswd(ctx context.Context, domain string, entries []string) (string, error) {
	var res TextResult
	err := c.Call(ctx, OpNginxWriteAuth, HtpasswdParams{Domain: domain, Entries: entries}, &res)
	return res.Text, err
}

func (c *Client) RemoveHtpasswd(ctx context.Context, domain string) error {
	return c.Call(ctx, OpNginxRemoveAuth, HtpasswdParams{Domain: domain}, nil)
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

// MkdirGroup legt ein Verzeichnis an, das einer anderen Gruppe als der des
// Eigentümers gehört — für alles, was der Webserver lesen können muss.
func (c *Client) MkdirGroup(ctx context.Context, path string, mode uint32, owner, group string) error {
	return c.Call(ctx, OpFileMkdir,
		FileMkdirParams{Path: path, Mode: mode, Owner: owner, Group: group}, nil)
}

// WriteFileGroup legt eine Datei an, die einer anderen Gruppe als der des
// Eigentümers gehört — für Dateien, die der Webserver ausliefern soll, ohne
// dass sie für alle lesbar sein müssen.
func (c *Client) WriteFileGroup(ctx context.Context, path, content string, mode uint32, owner, group string) error {
	return c.Call(ctx, OpFileWrite,
		FileWriteParams{Path: path, Content: content, Mode: mode, Owner: owner, Group: group}, nil)
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

// --- Prozesse --------------------------------------------------------------

func (c *Client) Processes(ctx context.Context) ([]ProcessInfo, error) {
	var res []ProcessInfo
	err := c.Call(ctx, OpSystemProcesses, nil, &res)
	return res, err
}

// StopProcess beendet einen Prozess. user ist der erwartete Eigentümer; der
// Agent lehnt ab, wenn der Prozess jemand anderem gehört.
func (c *Client) StopProcess(ctx context.Context, pid int, user, signal string) error {
	return c.Call(ctx, OpSystemProcessKill,
		ProcessKillParams{PID: pid, User: user, Signal: signal}, nil)
}

// RunQuery führt eine SQL-Anweisung gegen eine Datenbank aus.
func (c *Client) RunQuery(ctx context.Context, database, statement string,
	maxRows int) (*MySQLQueryResult, error) {

	var res MySQLQueryResult
	err := c.Call(ctx, OpMySQLQuery, MySQLQueryParams{
		Database: database, Statement: statement, MaxRows: maxRows,
	}, &res)
	return &res, err
}

// Traffic liest die Access-Logs ab dem übergebenen Lesestand.
func (c *Client) Traffic(ctx context.Context, cursors []TrafficCursor) (*TrafficResult, error) {
	var res TrafficResult
	err := c.Call(ctx, OpNginxTraffic, TrafficParams{Files: cursors}, &res)
	return &res, err
}

// --- Apps -------------------------------------------------------------------

// WriteApp schreibt Unit und Umgebung und startet die App.
func (c *Client) WriteApp(ctx context.Context, p AppParams) (*AppResult, error) {
	var res AppResult
	err := c.Call(ctx, OpAppWrite, p, &res)
	return &res, err
}

// RemoveApp hält die App an und räumt ihre Dateien weg. Das Verzeichnis der
// Site bleibt unangetastet — es gehört der Site, nicht der App.
func (c *Client) RemoveApp(ctx context.Context, name string) error {
	var res TextResult
	return c.Call(ctx, OpAppRemove, AppNameParams{Name: name}, &res)
}

// AppStatus sagt, was der Dienst wirklich tut — nicht, was das Panel glaubt.
func (c *Client) AppStatus(ctx context.Context, name string) (*AppResult, error) {
	var res AppResult
	err := c.Call(ctx, OpAppStatus, AppNameParams{Name: name}, &res)
	return &res, err
}

// AppRuntimes sagt, welche Laufzeitumgebungen der Server hat. Damit kann die
// Oberfläche "Node ist nicht installiert" schreiben, statt eine App anzulegen,
// die nicht startet.
func (c *Client) AppRuntimes(ctx context.Context) ([]RuntimeInfo, error) {
	var res []RuntimeInfo
	err := c.Call(ctx, OpAppRuntimes, nil, &res)
	return res, err
}

// --- Firewall und Fail2ban --------------------------------------------------

// FirewallStatusOf sagt, welche Firewall läuft und was offen ist.
func (c *Client) FirewallStatusOf(ctx context.Context) (*FirewallStatus, error) {
	var res FirewallStatus
	err := c.Call(ctx, OpFirewallStatus, nil, &res)
	return &res, err
}

// SetFirewallRule setzt oder entfernt eine ufw-Regel.
func (c *Client) SetFirewallRule(ctx context.Context, p FirewallRuleParams) (string, error) {
	var res TextResult
	err := c.Call(ctx, OpFirewallRule, p, &res)
	return res.Text, err
}

// Fail2banStatusOf liest die Jails und die gesperrten Adressen.
func (c *Client) Fail2banStatusOf(ctx context.Context) (*Fail2banStatus, error) {
	var res Fail2banStatus
	err := c.Call(ctx, OpFail2banStatus, nil, &res)
	return &res, err
}

// Unban hebt eine Sperre auf.
func (c *Client) Unban(ctx context.Context, jail, ip string) (string, error) {
	var res TextResult
	err := c.Call(ctx, OpFail2banUnban, Fail2banUnbanParams{Jail: jail, IP: ip}, &res)
	return res.Text, err
}

// --- Node-Fassungen ---------------------------------------------------------

// NodeVersions sagt, welche Fassungen installiert sind.
func (c *Client) NodeVersions(ctx context.Context) ([]NodeVersion, error) {
	var res []NodeVersion
	err := c.Call(ctx, OpNodeList, nil, &res)
	return res, err
}

// InstallNode holt eine Fassung und packt sie aus.
func (c *Client) InstallNode(ctx context.Context, version string) (*NodeVersion, error) {
	var res NodeVersion
	err := c.Call(ctx, OpNodeInstall, NodeInstallParams{Version: version}, &res)
	return &res, err
}

// RemoveNode entfernt eine Fassung.
func (c *Client) RemoveNode(ctx context.Context, major int) error {
	var res TextResult
	return c.Call(ctx, OpNodeRemove, map[string]int{"major": major}, &res)
}

// --- Docker -----------------------------------------------------------------

// DockerStatusOf sagt, ob Docker läuft und wie sicher es steht.
func (c *Client) DockerStatusOf(ctx context.Context) (*DockerStatus, error) {
	var res DockerStatus
	err := c.Call(ctx, OpDockerStatus, nil, &res)
	return &res, err
}

// WriteContainerEnv legt die Umgebungsdatei an, bevor der Container startet.
func (c *Client) WriteContainerEnv(ctx context.Context, p ContainerParams) error {
	var res TextResult
	return c.Call(ctx, OpDockerEnv, p, &res)
}

// RunContainer legt den Container an und startet ihn. Ein gleichnamiger wird
// vorher entfernt — die Operation ist damit wiederholbar.
func (c *Client) RunContainer(ctx context.Context, p ContainerParams) (*ContainerResult, error) {
	var res ContainerResult
	err := c.Call(ctx, OpDockerRun, p, &res)
	return &res, err
}

// ContainerAction hält an, startet, startet neu oder entfernt.
func (c *Client) ContainerAction(ctx context.Context, name, action string) error {
	var res TextResult
	return c.Call(ctx, OpDockerAction, map[string]string{
		"name": name, "action": action,
	}, &res)
}

// Containers listet ausschließlich die Container dieses Panels.
func (c *Client) Containers(ctx context.Context) ([]ContainerResult, error) {
	var res []ContainerResult
	err := c.Call(ctx, OpDockerList, nil, &res)
	return res, err
}

// ContainerLogs liefert die letzten Zeilen.
func (c *Client) ContainerLogs(ctx context.Context, name string, lines int) (string, error) {
	var res TextResult
	err := c.Call(ctx, OpDockerLogs, ContainerNameParams{Name: name, Lines: lines}, &res)
	return res.Text, err
}

// PullImage holt ein Image.
func (c *Client) PullImage(ctx context.Context, image string) (string, error) {
	var res TextResult
	err := c.Call(ctx, OpDockerPull, map[string]string{"image": image}, &res)
	return res.Text, err
}

// ContainerStatsOf fragt den Verbrauch der laufenden Container ab.
func (c *Client) ContainerStatsOf(ctx context.Context) ([]ContainerStats, error) {
	var res []ContainerStats
	err := c.Call(ctx, OpDockerStats, nil, &res)
	return res, err
}

// Images listet, was auf der Platte liegt.
func (c *Client) Images(ctx context.Context) ([]ImageInfo, error) {
	var res []ImageInfo
	err := c.Call(ctx, OpDockerImages, nil, &res)
	return res, err
}

// RemoveImage entfernt ein Image — ohne Gewalt, siehe Agent.
func (c *Client) RemoveImage(ctx context.Context, ref string) (string, error) {
	var res TextResult
	err := c.Call(ctx, OpDockerImageRemove, map[string]string{"ref": ref}, &res)
	return res.Text, err
}

// PortScanStatusOf sagt, ob die Port-Scan-Erkennung steht.
func (c *Client) PortScanStatusOf(ctx context.Context) (*PortScanStatus, error) {
	var res PortScanStatus
	err := c.Call(ctx, OpPortScanStatus, nil, &res)
	return &res, err
}

// SetPortScan schaltet die Erkennung ein oder aus.
func (c *Client) SetPortScan(ctx context.Context, p PortScanParams) (string, error) {
	var res TextResult
	err := c.Call(ctx, OpPortScanSet, p, &res)
	return res.Text, err
}

// --- Mail --------------------------------------------------------------------

// MailStatusOf sagt, was auf diesem Server für Mail bereitsteht.
func (c *Client) MailStatusOf(ctx context.Context) (*MailStatus, error) {
	var res MailStatus
	err := c.Call(ctx, OpMailStatus, nil, &res)
	return &res, err
}

// MailSetup richtet Benutzer, Verzeichnisse und Postfix-Einstellungen ein.
func (c *Client) MailSetup(ctx context.Context) (string, error) {
	var res TextResult
	err := c.Call(ctx, OpMailSetup, nil, &res)
	return res.Text, err
}

// MailFactsOf sammelt, was nur der Server über sich selbst weiß.
func (c *Client) MailFactsOf(ctx context.Context) (*MailFacts, error) {
	var res MailFacts
	err := c.Call(ctx, OpMailFacts, nil, &res)
	return &res, err
}

// RspamdStatsOf fragt Rspamds eigene Statistik ab — was tatsächlich als Spam
// gilt, nicht nur, dass der Milter eingetragen ist.
func (c *Client) RspamdStatsOf(ctx context.Context) (*RspamdStats, error) {
	var res RspamdStats
	err := c.Call(ctx, OpMailSpamStats, nil, &res)
	return &res, err
}

// WriteMailAutoconfig schreibt die Mozilla-/Microsoft-Konfiguration einer
// Maildomäne und liefert die beiden Dateipfade zurück, auf die der zugehörige
// Vhost verweisen muss.
func (c *Client) WriteMailAutoconfig(ctx context.Context, domain, host string,
	imapPort, smtpPort int) (mozillaPath, microsoftPath string, err error) {

	var res map[string]string
	err = c.Call(ctx, OpMailAutoconfig, AutoconfigParams{
		Domain: domain, Host: host, IMAPPort: imapPort, SMTPPort: smtpPort,
	}, &res)
	return res["mozilla_path"], res["microsoft_path"], err
}

// ApplyMail schreibt den vollständigen Sollzustand in die Map-Dateien.
func (c *Client) ApplyMail(ctx context.Context, p MailApplyParams) (string, error) {
	var res TextResult
	err := c.Call(ctx, OpMailApply, p, &res)
	return res.Text, err
}

// InstallFeature holt die Pakete einer Fähigkeit nach.
func (c *Client) InstallFeature(ctx context.Context, feature string) (string, error) {
	// Paketinstallationen dauern länger als die Vorgabe des Clients.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	var res TextResult
	err := c.callSlow(ctx, OpFeatureInstall, map[string]string{"feature": feature}, &res)
	return res.Text, err
}

// UninstallFeature entfernt die Pakete einer Fähigkeit wieder — über dieselbe
// feste Liste wie beim Installieren.
func (c *Client) UninstallFeature(ctx context.Context, feature string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	var res TextResult
	err := c.callSlow(ctx, OpFeatureUninstall, map[string]string{"feature": feature}, &res)
	return res.Text, err
}

// InstallWordPress holt den WordPress-Kern in eine bereits angelegte Site.
func (c *Client) InstallWordPress(ctx context.Context, p WordPressInstallParams) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, wordpressTimeout+time.Minute)
	defer cancel()

	var res TextResult
	err := c.callSlow(ctx, OpAppStoreWordPress, p, &res)
	return res.Text, err
}

// InstallWebmail holt Roundcube, konfiguriert es und spielt sein
// Datenbankschema ein.
func (c *Client) InstallWebmail(ctx context.Context, p WebmailInstallParams) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, roundcubeTimeout+time.Minute)
	defer cancel()

	var res TextResult
	err := c.callSlow(ctx, OpWebmailInstall, p, &res)
	return res.Text, err
}

// --- Git-Deploy -------------------------------------------------------------

// Deploy holt den Stand, baut ihn und schaltet um.
func (c *Client) Deploy(ctx context.Context, p DeployParams) (*DeployResult, error) {
	var res DeployResult
	err := c.Call(ctx, OpDeployRun, p, &res)
	return &res, err
}

// DeployKey liefert den öffentlichen Deploy-Key und legt ihn an, wenn es noch
// keinen gibt. Der private Teil verlässt den Server nie.
func (c *Client) DeployKey(ctx context.Context, name, systemUser string) (*DeployKeyResult, error) {
	var res DeployKeyResult
	err := c.Call(ctx, OpDeployKey, DeployKeyParams{Name: name, SystemUser: systemUser}, &res)
	return &res, err
}

// DeployList sagt, welche Stände dastehen und welcher gilt.
func (c *Client) DeployList(ctx context.Context, rootPath string) (*DeployListResult, error) {
	var res DeployListResult
	err := c.Call(ctx, OpDeployList, DeployListParams{RootPath: rootPath}, &res)
	return &res, err
}

// DeployRollback zeigt current auf einen älteren Stand.
func (c *Client) DeployRollback(ctx context.Context, systemUser, rootPath, release string) error {
	var res TextResult
	return c.Call(ctx, OpDeployRollback, DeployRollbackParams{
		SystemUser: systemUser, RootPath: rootPath, Release: release,
	}, &res)
}

// --- Echte Dateisystem-Quotas ----------------------------------------------

// QuotaStatus sagt, ob unter einem Pfad Project Quota möglich ist.
func (c *Client) QuotaStatus(ctx context.Context, path string) (*QuotaSupport, error) {
	var res QuotaSupport
	err := c.Call(ctx, OpQuotaStatus, QuotaStatusParams{Path: path}, &res)
	return &res, err
}

// SetQuotaProject hängt die Verzeichnisse eines Mandanten an seine
// Projektnummer und setzt darauf die Grenze.
func (c *Client) SetQuotaProject(ctx context.Context, tenant int64, dirs []string,
	limitMB int64) (*QuotaProjectResult, error) {

	var res QuotaProjectResult
	err := c.Call(ctx, OpQuotaProject, QuotaProjectParams{
		Tenant: tenant, Dirs: dirs, LimitMB: limitMB,
	}, &res)
	return &res, err
}

// --- Datenbankzugriff von außen --------------------------------------------

func (c *Client) MySQLRemoteStatus(ctx context.Context) (*MySQLRemoteResult, error) {
	var res MySQLRemoteResult
	err := c.Call(ctx, OpMySQLRemoteStatus, nil, &res)
	return &res, err
}

func (c *Client) SetMySQLRemote(ctx context.Context, enabled bool) (*MySQLRemoteResult, error) {
	var res MySQLRemoteResult
	err := c.Call(ctx, OpMySQLRemoteSet, MySQLRemoteParams{Enabled: enabled}, &res)
	return &res, err
}

// --- FTP -------------------------------------------------------------------

func (c *Client) FTPSetup(ctx context.Context) (*FTPSetupResult, error) {
	var res FTPSetupResult
	err := c.Call(ctx, OpFTPSetup, nil, &res)
	return &res, err
}

func (c *Client) FTPStatus(ctx context.Context) (*FTPSetupResult, error) {
	var res FTPSetupResult
	err := c.Call(ctx, OpFTPStatus, nil, &res)
	return &res, err
}

// SetFTPUser legt einen virtuellen Zugang an oder ändert ihn.
//
// UID und GID stehen nicht in den Parametern: der Agent schlägt sie zum
// Systembenutzer nach und meldet im Ergebnis zurück, womit der Zugang nun
// wirklich läuft.
func (c *Client) SetFTPUser(ctx context.Context, p FTPUserParams) (*FTPUserResult, error) {
	var res FTPUserResult
	err := c.Call(ctx, OpFTPUserSet, p, &res)
	return &res, err
}

func (c *Client) DeleteFTPUser(ctx context.Context, username string) error {
	return c.Call(ctx, OpFTPUserDelete, FTPUserParams{Username: username}, nil)
}

// FTPUsers liest, was der Dienst wirklich kennt — nicht, was das Panel glaubt.
func (c *Client) FTPUsers(ctx context.Context) ([]string, error) {
	var res []string
	err := c.Call(ctx, OpFTPUserList, nil, &res)
	return res, err
}

// --- Terminal --------------------------------------------------------------

// OpenTerminal startet eine Shell als user und liefert den Socket, über den die
// Sitzung läuft. Sie muss innerhalb weniger Sekunden abgeholt werden.
func (c *Client) OpenTerminal(ctx context.Context, user, dir string, cols, rows int) (*TerminalSession, error) {
	var res TerminalSession
	err := c.Call(ctx, OpTerminalOpen,
		TerminalParams{User: user, Dir: dir, Cols: cols, Rows: rows}, &res)
	return &res, err
}

func (c *Client) ResizeTerminal(ctx context.Context, session string, cols, rows int) error {
	return c.Call(ctx, OpTerminalResize,
		TerminalParams{Session: session, Cols: cols, Rows: rows}, nil)
}

func (c *Client) CloseTerminal(ctx context.Context, session string) error {
	return c.Call(ctx, OpTerminalClose, TerminalParams{Session: session}, nil)
}
