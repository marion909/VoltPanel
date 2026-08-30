# Architektur

## Die Zweiteilung

VoltPanel läuft als zwei Prozesse:

| | `volt-web` | `volt-agent` |
|---|---|---|
| Benutzer | `volt` (unprivilegiert) | `root` |
| Nimmt entgegen | HTTP, WebSocket | Unix-Socket |
| Kann | lesen, planen, in die Panel-DB schreiben | Systemzustand ändern |
| Kennt | die Domänenlogik | eine feste Liste von Operationen |

Der Grund steht in Prinzip 5 der Roadmap: **ein XSS im Panel darf nicht Root
bedeuten.** Wäre das Panel ein einziger Root-Prozess, wäre jede
Cross-Site-Scripting-Lücke im Frontend gleichbedeutend mit einem übernommenen
Server.

Die Trennung wirkt nur, wenn der Agent nichts Generisches anbietet. Deshalb gibt
es dort keine Operation `exec`, kein `shell`, kein `file.write` auf beliebige
Pfade. Die vollständige Liste steht in `internal/agent/protocol.go` und ist
gleichzeitig die Whitelist: `dispatch()` schlägt die Operation in einer Map nach,
und was nicht drinsteht, passiert nicht.

## Ablauf: eine Site anlegen

```
volt site add example.at --php 8.3
        │
        ├─ 1. store.CreateSite            Domain validieren, Zeile schreiben
        ├─ 2. agent.user.create           Linux-User site_example_at anlegen
        ├─ 3. agent.file.mkdir  (×4)      public/, tmp/, tmp/sessions/, logs/
        ├─ 4. agent.file.write            Platzhalterseite
        ├─ 5. templates.RenderPool        FPM-Pool aus dem DB-Stand erzeugen
        ├─ 6. agent.php.write_pool        ablegen, php8.3-fpm reload
        ├─ 7. templates.RenderSite        Vhost aus dem DB-Stand erzeugen
        └─ 8. agent.nginx.write_vhost     ablegen, nginx -t, reload
```

Die Reihenfolge ist so gewählt, dass sie umkehrbar bleibt: zuerst die Datenbank
(billig zurückzurollen), dann die Systemressourcen. Scheitert Schritt 6, räumt
`cleanup()` die Schritte 2–5 wieder ab — ein zweiter Versuch findet einen sauberen
Zustand vor.

Die Schritte 6 und 8 haben zusätzlich einen eigenen Rückzug: der Agent schreibt
die Datei, prüft (`nginx -t` bzw. der FPM-Reload), und bei einem Fehler steht
sofort wieder der vorherige Inhalt dort. Damit kann eine kaputte Config den
Webserver nicht beim nächsten Reload umwerfen — auch dann nicht, wenn den jemand
anders auslöst.

## Die Datenbank ist die Quelle der Wahrheit

Keine Config wird gepatcht. Jede entsteht vollständig neu aus dem, was in SQLite
steht. Daraus folgen drei Dinge:

- **Reparierbar.** `volt site rebuild --all` erzeugt den kompletten
  Webserver-Zustand neu, egal was vorher von Hand verbogen wurde.
- **Wiederherstellbar.** Ein Restore der Datenbank plus `rebuild` ergibt exakt
  denselben Server.
- **Diffbar.** Zwei Installationen mit gleichem Datenbestand erzeugen
  byteweise dieselben Configs (bis auf den Zeitstempel im Kopf).

## Tenant-Isolation

Der Scope (`internal/store/scope.go`) ist ein Pflichtparameter jeder
Repository-Methode. Sein Nullwert ist absichtlich unbrauchbar:

```go
var zero store.Scope
sites, err := st.ListSites(ctx, zero)   // → ErrNoTenant, keine Zeilen
```

Ein vergessener Scope liefert also einen Fehler, nicht versehentlich alle
Tenants. Über den Tenant hinaus schauen kann nur, wessen Rolle das darf —
`Elevate()` und `ForTenant()` prüfen das selbst und geben einer Kundenrolle den
unveränderten Scope zurück, statt sich auf eine Prüfung beim Aufrufer zu
verlassen.

Zusätzlich prüft `owns()` geladene Zeilen ein zweites Mal gegen den Scope. Das
ist der Gürtel zum Hosenträger der WHERE-Klausel: greift auch dort, wo eine
Query künftig einmal ohne `where()` geschrieben wird.

## Update und Rollback

```
volt update
   ├─ latest.json vom Kanal holen
   ├─ Snapshot: Binary + VACUUM INTO der DB + /etc/volt
   ├─ neues Binary laden, SHA-256 prüfen, atomar tauschen
   ├─ Migrationen fahren
   └─ bei Fehler: Binary und DB aus dem Snapshot zurück
```

Migrationen laufen nur vorwärts, jede in einer eigenen Transaktion. Ein Rollback
passiert nicht über Down-Migrationen — eine halb ausgeführte Down-Migration wäre
schlimmer als die Kopie —, sondern durch Zurückspielen des Snapshots.

Startet ein altes Binary gegen eine neuere Datenbank, bricht es beim Start ab,
statt gegen ein Schema zu arbeiten, das es nicht kennt.
