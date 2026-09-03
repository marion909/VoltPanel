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
	OpSystemUpdate     Op = "system.update"
	OpNginxWriteShared Op = "nginx.write_shared"
	OpNginxWriteAuth   Op = "nginx.write_htpasswd"
	OpNginxRemoveAuth  Op = "nginx.remove_htpasswd"

	// PHP-FPM
	OpPHPWritePool  Op = "php.write_pool"
	OpPHPRemovePool Op = "php.remove_pool"
	OpPHPReload     Op = "php.reload"
	OpPHPVersions   Op = "php.versions"
	OpPHPExtensions Op = "php.extensions"
	OpPHPExtInstall Op = "php.extension_install"
	OpPHPExtToggle  Op = "php.extension_toggle"

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

	// Prozesse
	OpSystemProcesses   Op = "system.processes"
	OpSystemProcessKill Op = "system.process_kill"

	// FTP: virtuelle Pure-FTPd-Zugaenge, kein zusaetzlicher Linux-Benutzer.
	OpFTPSetup      Op = "ftp.setup"
	OpFTPStatus     Op = "ftp.status"
	OpFTPUserSet    Op = "ftp.user_set"
	OpFTPUserDelete Op = "ftp.user_delete"
	OpFTPUserList   Op = "ftp.user_list"

	// Traffic aus den Nginx-Access-Logs, ab einem uebergebenen Lesestand.
	OpNginxTraffic Op = "nginx.traffic"

	// Apps: eine Anwendung als systemd-Unit hinter dem Reverse-Proxy.
	OpAppWrite    Op = "app.write"
	OpAppRemove   Op = "app.remove"
	OpAppStatus   Op = "app.status"
	OpAppRuntimes Op = "app.runtimes"

	// Firewall und Fail2ban. ufw schreibend, nftables nur lesend.
	OpFirewallStatus Op = "firewall.status"
	OpFirewallRule   Op = "firewall.rule"
	OpFail2banStatus Op = "fail2ban.status"
	OpFail2banUnban  Op = "fail2ban.unban"
	OpPortScanStatus Op = "firewall.portscan.status"
	OpPortScanSet    Op = "firewall.portscan.set"

	// Mail: das Panel beschreibt den Sollzustand, der Agent schreibt die
	// Map-Dateien und lädt die Dienste neu.
	OpMailStatus Op = "mail.status"
	OpMailSetup  Op = "mail.setup"
	OpMailApply  Op = "mail.apply"
	OpMailFacts  Op = "mail.facts"
	// OpMailSpamStats fragt Rspamds eigenen Controller nach seiner Statistik
	// — was tatsächlich als Spam gilt, nicht nur, dass der Milter eingetragen
	// ist.
	OpMailSpamStats Op = "mail.spamstats"
	// OpMailAutoconfig schreibt die Mozilla- und Microsoft-Konfiguration einer
	// Maildomäne als statische XML-Dateien. Ein eigener Vhost
	// (autoconfig.<domain> / autodiscover.<domain>) liefert sie danach
	// unverändert aus — hier entsteht nur der Inhalt, nicht die Config.
	OpMailAutoconfig Op = "mail.autoconfig"

	// OpFeatureInstall holt nach, was das Panel verwaltet — aus einer festen
	// Liste, nie mit einem Paketnamen aus der Anfrage. OpFeatureUninstall
	// nimmt es wieder herunter, über dieselbe Liste.
	OpFeatureInstall   Op = "feature.install"
	OpFeatureUninstall Op = "feature.uninstall"

	// OpAppStoreWordPress holt den WordPress-Kern in eine bereits angelegte
	// Site — der App-Store-Teil von Phase 7, siehe internal/core/appstore.go.
	OpAppStoreWordPress Op = "appstore.wordpress"

	// OpWebmailInstall holt Roundcube in eine bereits eingerichtete, aber
	// keinem Mandanten gehörende Installation — siehe internal/core/webmail.go.
	OpWebmailInstall Op = "webmail.install"

	// Node-Fassungen nebeneinander, systemweit unter /opt/volt/node.
	OpNodeList    Op = "node.list"
	OpNodeInstall Op = "node.install"
	OpNodeRemove  Op = "node.remove"

	// Docker. Der Aufrufer beschreibt, was er will; die Kommandozeile baut
	// der Agent. Es gibt kein Feld fuer einen Schalter.
	OpDockerStatus      Op = "docker.status"
	OpDockerRun         Op = "docker.run"
	OpDockerEnv         Op = "docker.env"
	OpDockerAction      Op = "docker.action"
	OpDockerList        Op = "docker.list"
	OpDockerLogs        Op = "docker.logs"
	OpDockerPull        Op = "docker.pull"
	OpDockerStats       Op = "docker.stats"
	OpDockerImages      Op = "docker.images"
	OpDockerImageRemove Op = "docker.image.remove"

	// Git-Deploy: holen, bauen, umschalten. Der Umschalter ist ein Symlink.
	OpDeployRun      Op = "deploy.run"
	OpDeployKey      Op = "deploy.key"
	OpDeployList     Op = "deploy.list"
	OpDeployRollback Op = "deploy.rollback"

	// Echte Dateisystem-Quotas ueber Project Quota (ext4/XFS).
	OpQuotaStatus  Op = "quota.status"
	OpQuotaProject Op = "quota.project"

	// SQL aus der Oberflaeche, unter einem auf eine Datenbank begrenzten Konto.
	OpMySQLQuery Op = "mysql.query"

	// Datenbankzugriff von aussen: horcht MariaDB ueberhaupt im Netz?
	OpMySQLRemoteStatus Op = "mysql.remote_status"
	OpMySQLRemoteSet    Op = "mysql.remote_set"

	// Terminal: eine Shell als Systembenutzer einer Site, nie als root.
	OpTerminalOpen   Op = "terminal.open"
	OpTerminalResize Op = "terminal.resize"
	OpTerminalClose  Op = "terminal.close"
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
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	// Input trennt "die Anfrage war falsch" von "die Operation ist
	// gescheitert". Ohne diese Unterscheidung kommt eine abgelehnte Eingabe
	// als Gateway-Fehler beim Benutzer an — mit einem Text, der so klingt,
	// als sei der Agent kaputt.
	Input  bool            `json:"input,omitempty"`
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

// SharedParams trägt die vhost-übergreifende Config. Sie hat keinen
// Domainnamen: ihr Ablageort steht fest, es gibt genau eine davon.
type SharedParams struct {
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

type ProcessKillParams struct {
	PID int `json:"pid"`
	// User ist der erwartete Eigentümer. Der Agent beendet den Prozess nur,
	// wenn er wirklich diesem Benutzer gehört — der Web-Prozess kennt den
	// Mandanten, der Agent kennt den Prozess.
	User   string `json:"user"`
	Signal string `json:"signal"` // TERM (Vorgabe) oder KILL
}

// TerminalParams eröffnet eine Sitzung. Der Benutzer kommt vom Web-Prozess aus
// der Site, nie aus der Anfrage des Browsers.
type TerminalParams struct {
	User    string `json:"user"`
	Dir     string `json:"dir"`
	Cols    int    `json:"cols"`
	Rows    int    `json:"rows"`
	Session string `json:"session"`
}

// FTPUserParams beschreibt einen virtuellen Zugang.
//
// UID und GID stehen hier bewusst nicht: der Agent schlaegt sie selbst zum
// Systembenutzer nach. Kaemen sie aus der Anfrage, waere "lege einen
// FTP-Zugang an" ein Weg, einen Zugang mit der UID von root zu bekommen — und
// damit vollen Zugriff auf den Server ueber ein FTP-Programm.
type FTPUserParams struct {
	Username string `json:"username"`
	Password string `json:"password"`
	// SystemUser ist der Site-Benutzer, unter dem der Zugang arbeitet. Er muss
	// mit site_ beginnen; der Agent prueft das noch einmal selbst.
	SystemUser string `json:"system_user"`
	HomeDir    string `json:"home_dir"`
	QuotaMB    int64  `json:"quota_mb"`
}

// FTPUserResult meldet zurueck, was tatsaechlich eingetragen wurde.
type FTPUserResult struct {
	Username string `json:"username"`
	UID      int    `json:"uid"`
	GID      int    `json:"gid"`
	HomeDir  string `json:"home_dir"`
	Created  bool   `json:"created"`
}

type CertInstallParams struct {
	Domain  string `json:"domain"`
	CertPEM string `json:"cert_pem"`
	KeyPEM  string `json:"key_pem"`
}

// --- Ergebnisstrukturen ----------------------------------------------------

// FTPSetupResult beschreibt den Zustand des Dienstes.
type FTPSetupResult struct {
	Installed   bool   `json:"installed"`
	Active      bool   `json:"active"`
	Ready       bool   `json:"ready"`
	PassiveFrom int    `json:"passive_from"`
	PassiveTo   int    `json:"passive_to"`
	TLSCert     string `json:"tls_cert"`
	// FirewallHint sagt, was mit den Ports geschehen ist oder noch geschehen
	// muss. Bei nftables kann der Agent nichts tun und sagt das auch.
	FirewallHint string `json:"firewall_hint"`
	// Notice steht da, wenn das Einrichten geklappt hat, dabei aber etwas
	// erwähnenswert schiefging — ein Erfolg mit Fussnote.
	Notice string `json:"notice"`
}

type ProcessInfo struct {
	PID        int     `json:"pid"`
	PPID       int     `json:"ppid"`
	User       string  `json:"user"`
	State      string  `json:"state"`
	CPUPercent float64 `json:"cpu_percent"`
	MemBytes   int64   `json:"mem_bytes"`
	Threads    int     `json:"threads"`
	Command    string  `json:"command"`
}

// TerminalSession ist der Rückweg zu einer eröffneten Shell: ein eigener
// Socket je Sitzung, über den der Web-Prozess die rohen Bytes durchreicht.
type TerminalSession struct {
	Session string `json:"session"`
	Socket  string `json:"socket"`
	User    string `json:"user"`
}

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

// UpdateResult beschreibt, was ein Update bewirkt hat.
//
// Es gibt keine UpdateParams: der Agent nimmt für das Update nichts entgegen.
// Welche Version installiert wird, ergibt sich allein aus dem Kanal in seiner
// eigenen Konfiguration. Dürfte der Web-Prozess eine Quelle mitgeben, wäre
// jede Übernahme des Panels ein Weg, beliebigen Code als root auszuführen.
// PHPExtension beschreibt ein Modul einer PHP-Version.
type PHPExtension struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	// Essential markiert Module, ohne die kein Pool mehr startet. Sie lassen
	// sich nicht abschalten — sonst wäre die Site tot und der Grund stünde
	// nirgends.
	Essential bool `json:"essential"`
}

// PHPExtParams trägt Version und Modulname. Der Paketname entsteht daraus im
// Agent; er kommt nie aus der Anfrage.
type PHPExtParams struct {
	PHPVersion string `json:"php_version"`
	Name       string `json:"name"`
	Enable     bool   `json:"enable"`
}

type UpdateResult struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Changed   bool   `json:"changed"`
	Restarted bool   `json:"restarted"`
	Output    string `json:"output"`
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
	// Input markiert Fehler, die an der Anfrage liegen und nicht am System.
	Input bool
}

func (e *OpError) Error() string { return fmt.Sprintf("%s: %s", e.Op, e.Message) }

func opErr(op Op, format string, args ...any) error {
	return &OpError{Op: op, Message: fmt.Sprintf(format, args...)}
}

// opInputErr meldet eine unbrauchbare Eingabe. Der Unterschied zu opErr ist
// nicht kosmetisch: er entscheidet, ob der Aufrufer einen 400 oder einen 502
// sieht — also ob er seine Eingabe korrigiert oder den Server sucht.
func opInputErr(op Op, format string, args ...any) error {
	return &OpError{Op: op, Message: fmt.Sprintf(format, args...), Input: true}
}
