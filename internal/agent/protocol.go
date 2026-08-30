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
