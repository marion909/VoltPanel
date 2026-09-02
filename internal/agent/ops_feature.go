package agent

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
)

// Nachinstallieren, was das Panel verwaltet.
//
// Das Panel meldete an einem halben Dutzend Stellen "X ist auf diesem Server
// nicht installiert" — und bot nichts an. Wer das liest, greift zur Shell, und
// genau dafür ist das Panel nicht da.
//
// Was hier *nicht* steht, ist ein Feld für einen Paketnamen. Der Aufrufer nennt
// eine Fähigkeit aus einer festen Liste; welche Pakete dazugehören, entscheidet
// diese Datei. Ein Paketname über die Leitung wäre `apt-get install` mit
// fremder Eingabe — und apt kennt Pakete, die Postinst-Skripte als root
// ausführen. Das ist kein Nachinstallieren mehr, das ist eine Rootshell.

// featurePakete ist die ganze Liste. Sie ist absichtlich kurz: hier gehört nur
// hinein, was das Panel danach auch verwaltet.
var featurePakete = map[string][]string{
	"docker":   {"docker.io"},
	"fail2ban": {"fail2ban"},
	// Postfix zieht bei der Installation einen debconf-Dialog hoch; aptInstall
	// setzt DEBIAN_FRONTEND=noninteractive, deshalb geht es durch. Die
	// Grundeinstellung danach macht `mail.setup`.
	"postfix":  {"postfix"},
	"dovecot":  {"dovecot-core", "dovecot-imapd"},
	"opendkim": {"opendkim", "opendkim-tools"},
	"rspamd":   {"rspamd"},
	// Node kommt nicht aus apt: das Panel führt eigene Fassungen unter
	// /opt/volt/node. Ein Paket daneben wäre eine zweite Wahrheit.
}

// FeatureNames sind die Fähigkeiten, die sich nachinstallieren lassen.
func FeatureNames() []string {
	out := make([]string, 0, len(featurePakete))
	for name := range featurePakete {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ValidFeature sagt, ob eine Fähigkeit bekannt ist.
func ValidFeature(name string) bool {
	_, ok := featurePakete[name]
	return ok
}

// opFeatureInstall installiert die Pakete einer Fähigkeit.
func (s *Server) opFeatureInstall(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[struct {
		Feature string `json:"feature"`
	}](raw, OpFeatureInstall)
	if err != nil {
		return nil, err
	}
	pakete, ok := featurePakete[p.Feature]
	if !ok {
		return nil, opInputErr(OpFeatureInstall,
			"%q ist keine bekannte fähigkeit — bekannt sind: %s",
			p.Feature, strings.Join(FeatureNames(), ", "))
	}

	out, err := s.aptInstall(ctx, pakete...)
	if err != nil {
		return nil, opErr(OpFeatureInstall, "%s installieren: %s",
			strings.Join(pakete, ", "), truncate(out, 500))
	}
	return TextResult{Text: strings.Join(pakete, ", ") + " installiert"}, nil
}
