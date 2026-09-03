package agent

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/marion909/voltpanel/internal/version"
)

const (
	shortTimeout = 15 * time.Second
	longTimeout  = 120 * time.Second
	// aptTimeout deckt Paketindex und Download ab. Ein Abbruch mittendrin
	// hinterlässt eine halb konfigurierte Paketdatenbank — teurer als warten.
	aptTimeout = 10 * time.Minute
)

// newRegistry ist die Whitelist des Agents: exakt diese Operationen existieren.
// Alles andere beantwortet dispatch() mit "unbekannte operation".
func (s *Server) newRegistry() map[Op]Handler {
	return map[Op]Handler{
		OpPing:        s.opPing,
		OpSystemInfo:  s.opSystemInfo,
		OpDiskUsage:   s.opDiskUsage,
		OpServiceList: s.opServiceList,

		OpServiceStatus:  s.opServiceStatus,
		OpServiceStart:   s.serviceAction("start"),
		OpServiceStop:    s.serviceAction("stop"),
		OpServiceRestart: s.serviceAction("restart"),
		OpServiceReload:  s.serviceAction("reload"),
		OpServiceEnable:  s.serviceAction("enable"),
		OpServiceDisable: s.serviceAction("disable"),

		OpNginxWriteVhost:  s.opNginxWriteVhost,
		OpNginxRemoveVhost: s.opNginxRemoveVhost,
		OpNginxTest:        s.opNginxTest,
		OpNginxReload:      s.opNginxReload,
		OpNginxWriteShared: s.opNginxWriteShared,
		OpSystemUpdate:     s.opSystemUpdate,
		OpPHPExtensions:    s.opPHPExtensions,
		OpPHPExtInstall:    s.opPHPExtInstall,
		OpPHPExtToggle:     s.opPHPExtToggle,

		OpSystemProcesses:   s.opSystemProcesses,
		OpSystemProcessKill: s.opSystemProcessKill,

		OpFTPSetup:      s.opFTPSetup,
		OpFTPStatus:     s.opFTPStatus,
		OpFTPUserSet:    s.opFTPUserSet,
		OpFTPUserDelete: s.opFTPUserDelete,
		OpFTPUserList:   s.opFTPUserList,

		OpNginxTraffic:      s.opNginxTraffic,
		OpAppWrite:          s.opAppWrite,
		OpAppRemove:         s.opAppRemove,
		OpAppStatus:         s.opAppStatus,
		OpAppRuntimes:       s.opAppRuntimes,
		OpFirewallStatus:    s.opFirewallStatus,
		OpFirewallRule:      s.opFirewallRule,
		OpFail2banStatus:    s.opFail2banStatus,
		OpFail2banUnban:     s.opFail2banUnban,
		OpPortScanStatus:    s.opPortScanStatus,
		OpPortScanSet:       s.opPortScanSet,
		OpMailStatus:        s.opMailStatus,
		OpMailSetup:         s.opMailSetup,
		OpMailApply:         s.opMailApply,
		OpMailFacts:         s.opMailFacts,
		OpMailSpamStats:     s.opMailSpamStats,
		OpMailAutoconfig:    s.opMailAutoconfig,
		OpFeatureInstall:    s.opFeatureInstall,
		OpFeatureUninstall:  s.opFeatureUninstall,
		OpAppStoreWordPress: s.opAppStoreWordPress,
		OpWebmailInstall:    s.opWebmailInstall,
		OpNodeList:          s.opNodeList,
		OpNodeInstall:       s.opNodeInstall,
		OpNodeRemove:        s.opNodeRemove,
		OpDockerStatus:      s.opDockerStatus,
		OpDockerRun:         s.opDockerRun,
		OpDockerEnv:         s.opDockerEnv,
		OpDockerAction:      s.opDockerAction,
		OpDockerList:        s.opDockerList,
		OpDockerLogs:        s.opDockerLogs,
		OpDockerPull:        s.opDockerPull,
		OpDockerStats:       s.opDockerStats,
		OpDockerImages:      s.opDockerImages,
		OpDockerImageRemove: s.opDockerImageRemove,
		OpDeployRun:         s.opDeployRun,
		OpDeployKey:         s.opDeployKey,
		OpDeployList:        s.opDeployList,
		OpDeployRollback:    s.opDeployRollback,
		OpQuotaStatus:       s.opQuotaStatus,
		OpQuotaProject:      s.opQuotaProject,
		OpMySQLQuery:        s.opMySQLQuery,
		OpMySQLRemoteStatus: s.opMySQLRemoteStatus,
		OpMySQLRemoteSet:    s.opMySQLRemoteSet,

		OpTerminalOpen:   s.opTerminalOpen,
		OpTerminalResize: s.opTerminalResize,
		OpTerminalClose:  s.opTerminalClose,

		OpNginxWriteAuth:  s.opNginxWriteAuth,
		OpNginxRemoveAuth: s.opNginxRemoveAuth,

		OpPHPWritePool:  s.opPHPWritePool,
		OpPHPRemovePool: s.opPHPRemovePool,
		OpPHPReload:     s.opPHPReload,
		OpPHPVersions:   s.opPHPVersions,

		OpUserCreate: s.opUserCreate,
		OpUserDelete: s.opUserDelete,
		OpUserExists: s.opUserExists,

		OpFileWrite:   s.opFileWrite,
		OpFileRead:    s.opFileRead,
		OpFileRemove:  s.opFileRemove,
		OpFileMkdir:   s.opFileMkdir,
		OpFileChown:   s.opFileChown,
		OpFileList:    s.opFileList,
		OpFileTailLog: s.opFileTailLog,
		OpFileMove:    s.opFileMove,
		OpFileCopy:    s.opFileCopy,
		OpFileChmod:   s.opFileChmod,
		OpFileStat:    s.opFileStat,
		OpFileArchive: s.opFileArchive,
		OpFileExtract: s.opFileExtract,

		OpFileReadChunk:  s.opFileReadChunk,
		OpFileWriteChunk: s.opFileWriteChunk,

		OpMySQLCreateDB:    s.opMySQLCreateDB,
		OpMySQLDropDB:      s.opMySQLDropDB,
		OpMySQLCreateUser:  s.opMySQLCreateUser,
		OpMySQLDropUser:    s.opMySQLDropUser,
		OpMySQLGrant:       s.opMySQLGrant,
		OpMySQLSetPassword: s.opMySQLSetPassword,
		OpMySQLSizes:       s.opMySQLSizes,
		OpMySQLDump:        s.opMySQLDump,
		OpMySQLImport:      s.opMySQLImport,

		OpCronWrite:  s.opCronWrite,
		OpCronRemove: s.opCronRemove,
		OpCronLog:    s.opCronLog,

		OpCertInstall: s.opCertInstall,
	}
}

func (s *Server) opPing(context.Context, json.RawMessage) (any, error) {
	return TextResult{Text: "pong " + version.Version}, nil
}

func (s *Server) opServiceStatus(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[ServiceParams](raw, OpServiceStatus)
	if err != nil {
		return nil, err
	}
	if err := checkService(p.Name); err != nil {
		return nil, err
	}
	return s.serviceStatus(ctx, p.Name), nil
}

// serviceStatus fragt systemd ab. Ein nicht installierter Dienst ist kein
// Fehler — das Dashboard zeigt ihn dann einfach als "nicht installiert".
func (s *Server) serviceStatus(ctx context.Context, name string) ServiceStatus {
	st := ServiceStatus{Name: name}

	// `show` liefert alles in einem Aufruf und schlägt bei unbekannten Units
	// nicht fehl, anders als `status`.
	out, err := run(ctx, shortTimeout, "systemctl", "show", name,
		"--property=LoadState,ActiveState,SubState,UnitFileState,Description", "--no-pager")
	if err != nil {
		return st
	}

	for _, line := range strings.Split(out, "\n") {
		key, val, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "LoadState":
			st.Installed = val == "loaded"
		case "ActiveState":
			st.Active = val == "active"
		case "SubState":
			st.SubState = val
		case "UnitFileState":
			st.Enabled = val == "enabled" || val == "enabled-runtime"
		case "Description":
			st.Description = val
		}
	}
	return st
}

// serviceAction erzeugt den Handler für start/stop/restart/… Alle teilen sich
// dieselbe Validierung, damit keine Variante versehentlich ohne auskommt.
func (s *Server) serviceAction(action string) Handler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[ServiceParams](raw, Op("service."+action))
		if err != nil {
			return nil, err
		}
		if err := checkService(p.Name); err != nil {
			return nil, err
		}
		// Bei start, restart und reload wird der Grund mitgeliefert. Bei stop
		// und disable gibt es keinen Dienst mehr, dessen Journal etwas sagen
		// könnte.
		switch action {
		case "start", "restart", "reload":
			if err := s.startService(ctx, Op("service."+action), p.Name, action); err != nil {
				return nil, err
			}
		default:
			if _, err := run(ctx, longTimeout, "systemctl", action, p.Name); err != nil {
				return nil, err
			}
		}
		return s.serviceStatus(ctx, p.Name), nil
	}
}

// opServiceList gibt den Zustand aller verwaltbaren Dienste zurück — die Basis
// für die Software-Kacheln im Dashboard.
func (s *Server) opServiceList(ctx context.Context, _ json.RawMessage) (any, error) {
	names := make([]string, 0, len(allowedServices))
	for n := range allowedServices {
		names = append(names, n)
	}
	for _, v := range detectPHPVersions(s.phpDir) {
		names = append(names, "php"+v+"-fpm")
	}

	out := make([]ServiceStatus, 0, len(names))
	for _, n := range names {
		st := s.serviceStatus(ctx, n)
		if st.Installed {
			out = append(out, st)
		}
	}
	return out, nil
}
