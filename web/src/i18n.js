import { reactive, computed } from 'vue'

// Gerüst für die Mehrsprachigkeit: die Zeichenketten liegen zentral, damit
// später weitere Sprachen dazukommen können, ohne die Komponenten anzufassen.
const messages = {
  de: {
    'nav.dashboard': 'Übersicht',
    'nav.sites': 'Websites',
    'nav.services': 'Dienste',
    'nav.audit': 'Protokoll',
    'nav.settings': 'Einstellungen',
    'nav.logout': 'Abmelden',

    'login.title': 'Anmelden',
    'login.email': 'E-Mail',
    'login.password': 'Passwort',
    'login.totp': 'Code aus der Authenticator-App',
    'login.submit': 'Anmelden',
    'login.setupHint': 'Es ist noch kein Benutzer eingerichtet. Auf dem Server: volt setup',

    'dash.cpu': 'CPU',
    'dash.memory': 'Arbeitsspeicher',
    'dash.disk': 'Speicherplatz',
    'dash.load': 'Last',
    'dash.traffic': 'Netzwerkdurchsatz',
    'dash.in': 'Eingehend',
    'dash.out': 'Ausgehend',
    'dash.sites': 'Websites',
    'dash.certs': 'Zertifikate',
    'dash.expiring': 'laufen bald ab',
    'dash.uptime': 'Laufzeit',
    'dash.processes': 'Prozesse',
    'dash.noData': 'Noch keine Messwerte',
    'dash.table': 'Als Tabelle anzeigen',
    'dash.hideTable': 'Tabelle ausblenden',
    'dash.time': 'Zeit',

    'sites.title': 'Websites',
    'sites.new': 'Neue Website',
    'sites.domain': 'Domain',
    'sites.type': 'Typ',
    'sites.php': 'PHP',
    'sites.ssl': 'SSL',
    'sites.status': 'Status',
    'sites.empty': 'Noch keine Website angelegt.',
    'sites.create': 'Anlegen',
    'sites.rebuild': 'Config neu erzeugen',
    'sites.delete': 'Entfernen',
    'sites.confirmDelete': 'Website {domain} wirklich entfernen? Die Dateien bleiben erhalten.',

    'services.title': 'Dienste',
    'services.running': 'läuft',
    'services.stopped': 'gestoppt',
    'services.autostart': 'Autostart',

    'audit.title': 'Protokoll',
    'audit.time': 'Zeitpunkt',
    'audit.actor': 'Benutzer',
    'audit.action': 'Aktion',
    'audit.target': 'Ziel',
    'audit.result': 'Ergebnis',
    'audit.empty': 'Noch keine Einträge.',

    'settings.title': 'Einstellungen',
    'settings.account': 'Konto',
    'settings.password': 'Passwort ändern',
    'settings.currentPassword': 'Aktuelles Passwort',
    'settings.newPassword': 'Neues Passwort',
    'settings.twofa': 'Zwei-Faktor-Anmeldung',
    'settings.twofaOn': 'Aktiv',
    'settings.twofaOff': 'Nicht eingerichtet',
    'settings.twofaSetup': 'Einrichten',
    'settings.twofaEnable': 'Aktivieren',
    'settings.twofaDisable': 'Abschalten',
    'settings.twofaScan': 'QR-Code in der Authenticator-App scannen und den angezeigten Code eingeben.',
    'settings.theme': 'Darstellung',
    'settings.themeSystem': 'System',
    'settings.themeLight': 'Hell',
    'settings.themeDark': 'Dunkel',
    'settings.language': 'Sprache',

    'common.save': 'Speichern',
    'common.cancel': 'Abbrechen',
    'common.loading': 'Wird geladen …',
    'common.error': 'Fehler',
    'common.yes': 'ja',
    'common.no': 'nein',
    'common.actions': 'Aktionen',
    'common.mustChangePassword': 'Bitte ändere dein Passwort — es wurde automatisch erzeugt.',
  },

  en: {
    'nav.dashboard': 'Overview',
    'nav.sites': 'Websites',
    'nav.services': 'Services',
    'nav.audit': 'Audit log',
    'nav.settings': 'Settings',
    'nav.logout': 'Sign out',

    'login.title': 'Sign in',
    'login.email': 'Email',
    'login.password': 'Password',
    'login.totp': 'Authenticator code',
    'login.submit': 'Sign in',
    'login.setupHint': 'No user has been set up yet. On the server: volt setup',

    'dash.cpu': 'CPU',
    'dash.memory': 'Memory',
    'dash.disk': 'Disk',
    'dash.load': 'Load',
    'dash.traffic': 'Network throughput',
    'dash.in': 'Inbound',
    'dash.out': 'Outbound',
    'dash.sites': 'Websites',
    'dash.certs': 'Certificates',
    'dash.expiring': 'expiring soon',
    'dash.uptime': 'Uptime',
    'dash.processes': 'Processes',
    'dash.noData': 'No measurements yet',
    'dash.table': 'Show as table',
    'dash.hideTable': 'Hide table',
    'dash.time': 'Time',

    'sites.title': 'Websites',
    'sites.new': 'New website',
    'sites.domain': 'Domain',
    'sites.type': 'Type',
    'sites.php': 'PHP',
    'sites.ssl': 'SSL',
    'sites.status': 'Status',
    'sites.empty': 'No website created yet.',
    'sites.create': 'Create',
    'sites.rebuild': 'Rebuild config',
    'sites.delete': 'Remove',
    'sites.confirmDelete': 'Really remove {domain}? The files will be kept.',

    'services.title': 'Services',
    'services.running': 'running',
    'services.stopped': 'stopped',
    'services.autostart': 'Autostart',

    'audit.title': 'Audit log',
    'audit.time': 'Time',
    'audit.actor': 'User',
    'audit.action': 'Action',
    'audit.target': 'Target',
    'audit.result': 'Result',
    'audit.empty': 'No entries yet.',

    'settings.title': 'Settings',
    'settings.account': 'Account',
    'settings.password': 'Change password',
    'settings.currentPassword': 'Current password',
    'settings.newPassword': 'New password',
    'settings.twofa': 'Two-factor authentication',
    'settings.twofaOn': 'Enabled',
    'settings.twofaOff': 'Not set up',
    'settings.twofaSetup': 'Set up',
    'settings.twofaEnable': 'Enable',
    'settings.twofaDisable': 'Disable',
    'settings.twofaScan': 'Scan the QR code in your authenticator app and enter the code shown.',
    'settings.theme': 'Appearance',
    'settings.themeSystem': 'System',
    'settings.themeLight': 'Light',
    'settings.themeDark': 'Dark',
    'settings.language': 'Language',

    'common.save': 'Save',
    'common.cancel': 'Cancel',
    'common.loading': 'Loading …',
    'common.error': 'Error',
    'common.yes': 'yes',
    'common.no': 'no',
    'common.actions': 'Actions',
    'common.mustChangePassword': 'Please change your password — it was generated automatically.',
  },
}

export const i18n = reactive({
  locale: localStorage.getItem('volt.locale') || 'de',
})

export function setLocale(locale) {
  if (!messages[locale]) return
  i18n.locale = locale
  localStorage.setItem('volt.locale', locale)
  document.documentElement.lang = locale
}

// t gibt bei fehlender Übersetzung den Schlüssel zurück — eine fehlende Zeile
// fällt so auf, statt leer zu bleiben.
export function t(key, params) {
  let text = messages[i18n.locale]?.[key] ?? messages.de[key] ?? key
  if (params) {
    for (const [name, value] of Object.entries(params)) {
      text = text.replace(`{${name}}`, value)
    }
  }
  return text
}

export const availableLocales = computed(() => Object.keys(messages))
