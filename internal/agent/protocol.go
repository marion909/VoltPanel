// Package agent definiert das Protokoll zwischen volt-web (unprivilegiert) und
// volt-agent (root) sowie beide Enden davon.
//
// Der Agent kennt keine generischen Shell-Kommandos. Er kennt eine feste Liste
// typisierter Operationen, jede mit einer eigenen Parameterstruktur, die vor der
// Ausführung validiert wird. Was hier nicht steht, kann der Web-Prozess nicht
// auslösen — auch nicht, wenn er vollständig übernommen wurde.
package agent

import (
	"encoding/json"
	"fmt"
)

// ProtocolVersion wird bei jedem Verbindungsaufbau abgeglichen. Passt sie nicht,
// bricht die Verbindung ab — ein halb aktualisiertes Paar aus Web und Agent
// soll gar nicht erst miteinander reden.
const ProtocolVersion = 1

type Op string

const (
	// Diagnose
	OpPing        Op = "ping"
	OpSystemInfo  Op = "system.info"
	OpDiskUsage   Op = "system.disk_usage"
	OpServiceList Op = "service.list"

	// Dienste
	OpServiceStatus  Op = "service.status"
	OpServiceStart   Op = "service.start"
	OpServiceStop    Op = "service.stop"
	OpServiceRestart Op = "service.restart"
	OpServiceReload  Op = "service.reload"
	OpServiceEnable  Op = "service.enable"
	OpServiceDisable Op = "service.disable"

	// Nginx
	OpNginxWriteVhost  Op = "nginx.write_vhost"
	OpNginxRemoveVhost Op = "nginx.remove_vhost"
	OpNginxTest        Op = "nginx.test"
	OpNginxReload      Op = "nginx.reload"
	OpNginxWriteAuth   Op = "nginx.write_htpasswd"
	OpNginxRemoveAuth  Op = "nginx.remove_htpasswd"

	// PHP-FPM
	OpPHPWritePool  Op = "php.write_pool"
	OpPHPRemovePool Op = "php.remove_pool"
	OpPHPReload     Op = "php.reload"
	OpPHPVersions   Op = "php.versions"

	// Systembenutzer
	OpUserCreate Op = "user.create"
	OpUserDelete Op = "user.delete"
	OpUserExists Op = "user.exists"

	// Dateien (streng auf erlaubte Wurzeln eingesperrt)
	OpFileWrite   Op = "file.write"
	OpFileRead    Op = "file.read"
	OpFileRemove  Op = "file.remove"
	OpFileMkdir   Op = "file.mkdir"
	OpFileChown   Op = "file.chown"
	OpFileList    Op = "file.list"
	OpFileTailLog Op = "file.tail_log"

	// Dateien: Umbenennen, Kopieren, Rechte, Archive
	OpFileMove    Op = "file.move"
	OpFileCopy    Op = "file.copy"
	OpFileChmod   Op = "file.chmod"
	OpFileStat    Op = "file.stat"
	OpFileArchive Op = "file.archive"
	OpFileExtract Op = "file.extract"

	// Blockweises Lesen und Schreiben für Up- und Download großer Dateien
	OpFileReadChunk  Op = "file.read_chunk"
	OpFileWriteChunk Op = "file.write_chunk"

	// Datenbanken
	OpMySQLCreateDB    Op = "mysql.create_db"
	OpMySQLDropDB      Op = "mysql.drop_db"
	OpMySQLCreateUser  Op = "mysql.create_user"
	OpMySQLDropUser    Op = "mysql.drop_user"
	OpMySQLGrant       Op = "mysql.grant"
	OpMySQLSetPassword Op = "mysql.set_password"
	OpMySQLSizes       Op = "mysql.sizes"
	OpMySQLDump        Op = "mysql.dump"
	OpMySQLImport      Op = "mysql.import"

	// Cronjobs
	OpCronWrite  Op = "cron.write"
	OpCronRemove Op = "cron.remove"
	OpCronLog    Op = "cron.log"

	// Zertifikate
	OpCertInstall Op = "cert.install"
)

// Request ist eine einzelne Anfrage an den Agent.
type Request struct {
	ID     string          `json:"id"`
	Op     Op              `json:"op"`
	Params json.RawMessage `json:"params,omitempty"`

	// Actor wandert nur ins Log des Agents, damit sich eine Root-Aktion später
	// einem Panel-User zuordnen lässt. Autorisiert wird damit nichts.
	Actor string `json:"actor,omitempty"`
}

// Response ist die Antwort auf genau eine Request-ID.
type Response struct {
	ID     string          `json:"id"`
	OK     bool            `json:"ok"`
	Error  string          `json:"error,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
}

// Hello geht als erste Zeile über eine neue Verbindung.
type Hello struct {
	Protocol int    `json:"protocol"`
	Client   string `json:"client"`
}

type HelloAck struct {
	Protocol int    `json:"protocol"`
	Agent    string `json:"agent"`
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
}

// --- Parameterstrukturen ---------------------------------------------------

type ServiceParams struct {
	Name string `json:"name"`
}

type VhostParams struct {
	Domain  string `json:"domain"`
	Content string `json:"content"`
}

// HtpasswdParams trägt fertige Hashes, keine Klartextpasswörter: das Hashen
// passiert im Web-Prozess, damit ein Klartextpasswort nie über den Socket geht
// und nie in einem Agent-Log landen kann.
type HtpasswdParams struct {
	Domain string `json:"domain"`
	// Entries sind Zeilen der Form "benutzer:$2a$...".
	Entries []string `json:"entries"`
}

type PoolParams struct {
	PHPVersion string `json:"php_version"`
	PoolName   string `json:"pool_name"`
	Content    string `json:"content"`
}

type UserCreateParams struct {
	Username string `json:"username"`
	HomeDir  string `json:"home_dir"`
	Shell    string `json:"shell"`
}

type UserDeleteParams struct {
	Username   string `json:"username"`
	RemoveHome bool   `json:"remove_home"`
}

type FileWriteParams struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Mode    uint32 `json:"mode"`
	Owner   string `json:"owner"`
	Group   string `json:"group"`
}

type FilePathParams struct {
	Path      string `json:"path"`
	Recursive bool   `json:"recursive"`
}

type FileChownParams struct {
	Path      string `json:"path"`
	Owner     string `json:"owner"`
	Group     string `json:"group"`
	Recursive bool   `json:"recursive"`
}

type FileMkdirParams struct {
	Path  string `json:"path"`
	Mode  uint32 `json:"mode"`
	Owner string `json:"owner"`
	Group string `json:"group"`
}

type TailParams struct {
	Path  string `json:"path"`
	Lines int    `json:"lines"`
}

type FileMoveParams struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Overwrite bool   `json:"overwrite"`
}

type FileChmodParams struct {
	Path      string `json:"path"`
	Mode      uint32 `json:"mode"`
	Recursive bool   `json:"recursive"`
}

type ChunkParams struct {
	Path   string `json:"path"`
	Offset int64  `json:"offset"`
	Length int    `json:"length"`
	// Data ist base64-kodiert: JSON verträgt keine rohen Bytes.
	Data  string `json:"data,omitempty"`
	Owner string `json:"owner,omitempty"`
	// Truncate leert die Datei vor dem Schreiben — der erste Block eines Uploads.
	Truncate bool `json:"truncate,omitempty"`
}

type ChunkResult struct {
	Data string `json:"data"`
	// EOF sagt dem Aufrufer, dass er aufhören kann zu fragen.
	EOF       bool  `json:"eof"`
	Size      int64 `json:"size"`
	BytesRead int   `json:"bytes_read"`
}

type ArchiveParams struct {
	// Sources sind die zu packenden Pfade, Dest ist das Archiv (.tar.gz oder .zip).
	Sources []string `json:"sources"`
	Dest    string   `json:"dest"`
	Owner   string   `json:"owner"`
}

type ExtractParams struct {
	Archive string `json:"archive"`
	Dest    string `json:"dest"`
	Owner   string `json:"owner"`
}

type MySQLDBParams struct {
	Name      string `json:"name"`
	Charset   string `json:"charset"`
	Collation string `json:"collation"`
}

type MySQLUserParams struct {
	Username    string `json:"username"`
	HostPattern string `json:"host_pattern"`
	Database    string `json:"database"`
	Grants      string `json:"grants"`
	Password    string `json:"password"`
}

type MySQLDumpParams struct {
	Database string `json:"database"`
	Path     string `json:"path"`
}

type CronParams struct {
	Name     string `json:"name"`
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
	RunAs    string `json:"run_as"`
	Lines    int    `json:"lines"`
}

type CertInstallParams struct {
	Domain  string `json:"domain"`
	CertPEM string `json:"cert_pem"`
	KeyPEM  string `json:"key_pem"`
}

// --- Ergebnisstrukturen ----------------------------------------------------

type ServiceStatus struct {
	Name        string `json:"name"`
	Installed   bool   `json:"installed"`
	Active      bool   `json:"active"`
	Enabled     bool   `json:"enabled"`
	SubState    string `json:"sub_state"`
	Description string `json:"description"`
}

type StatResult struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	Mode    string `json:"mode"`
	IsDir   bool   `json:"is_dir"`
	ModTime int64  `json:"mod_time"`
	Owner   string `json:"owner"`
	Group   string `json:"group"`
}

type FileEntry struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	Mode    string `json:"mode"`
	IsDir   bool   `json:"is_dir"`
	ModTime int64  `json:"mod_time"`
	Owner   string `json:"owner"`
	Group   string `json:"group"`
}

type TextResult struct {
	Text string `json:"text"`
}

type SystemInfo struct {
	Hostname     string   `json:"hostname"`
	OS           string   `json:"os"`
	Platform     string   `json:"platform"`
	Kernel       string   `json:"kernel"`
	Arch         string   `json:"arch"`
	Uptime       uint64   `json:"uptime"`
	AgentVersion string   `json:"agent_version"`
	PHPVersions  []string `json:"php_versions"`
}

// OpError ist ein Fehler, den der Agent bewusst und formuliert zurückgibt.
type OpError struct {
	Op      Op
	Message string
}

func (e *OpError) Error() string { return fmt.Sprintf("%s: %s", e.Op, e.Message) }

func opErr(op Op, format string, args ...any) error {
	return &OpError{Op: op, Message: fmt.Sprintf(format, args...)}
}
