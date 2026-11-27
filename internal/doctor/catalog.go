// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

// texts are the wordings of every check, by key (see texts.go). German
// follows web/src/locales/README.md: "du", short sentences, product names
// as they are.
var texts = map[string]text{
	// Shared.
	"read.failed":         {"cannot read {{path}}", "{{path}} kann nicht gelesen werden"},
	"read.error":          {"cannot read {{path}}: {{error}}", "{{path}} kann nicht gelesen werden: {{error}}"},
	"path.error":          {"{{path}}: {{error}}", "{{path}}: {{error}}"},
	"installer.step.hint": {"Run the installer again (step {{step}}).", "Führ den Installer erneut aus (Schritt {{step}})."},
	"installer.hint":      {"Run the installer again.", "Führ den Installer erneut aus."},
	"service.running":     {"{{name}} is running", "{{name}} läuft"},
	"socket.down":         {"{{name}} does not answer on {{path}}: {{error}}", "{{name}} antwortet nicht auf {{path}}: {{error}}"},

	// system.os
	"os.supported":        {"{{name}} is supported", "{{name}} wird unterstützt"},
	"os.unsupported":      {"{{name}} is not in the support matrix", "{{name}} steht nicht in der Support-Matrix"},
	"os.supported.hint":   {"Hostashi supports Debian 13 and Ubuntu 26.04.", "Hostashi unterstützt Debian 13 und Ubuntu 26.04."},
	"os.unsupported.hint": {"Hostashi supports Debian 13 and Ubuntu 26.04. Other systems are untested.", "Hostashi unterstützt Debian 13 und Ubuntu 26.04. Andere Systeme sind nicht getestet."},

	// system.disk
	"disk.free":       {"{{path}}: {{free}} free of {{total}} ({{percent}} %)", "{{path}}: {{free}} frei von {{total}} ({{percent}} %)"},
	"disk.error.hint": {"Check that the directory exists and its file system is mounted.", "Prüf, ob das Verzeichnis existiert und sein Dateisystem eingehängt ist."},
	"disk.fail.hint":  {"Free space now: sites, databases and backups fail when the disk is full.", "Schaff jetzt Platz: Sites, Datenbanken und Backups schlagen fehl, wenn die Platte voll ist."},
	"disk.warn.hint":  {"The disk is filling up. Check the sites' disk use and old backups.", "Die Platte füllt sich. Prüf die Belegung der Sites und alte Backups."},

	// system.quota
	"quota.error":         {"{{path}}: cannot read the quota state: {{error}}", "{{path}}: Der Quota-Zustand kann nicht gelesen werden: {{error}}"},
	"quota.enforced":      {"{{path}}: disk limits are enforced (project quota)", "{{path}}: Speicherlimits werden durchgesetzt (Project Quota)"},
	"quota.counting":      {"{{path}}: project quota only counts usage, disk limits are not enforced", "{{path}}: Project Quota zählt nur die Belegung, Speicherlimits werden nicht durchgesetzt"},
	"quota.counting.hint": {"Restart hostashi-agent: it switches enforcement on. If this stays, check the agent log.", "Starte hostashi-agent neu, dann schaltet er die Durchsetzung ein. Bleibt es so, prüf das Log des Agents."},
	"quota.off":           {"{{path}}: no project quota, sites get no disk limit", "{{path}}: kein Project Quota, Sites bekommen kein Speicherlimit"},
	"quota.off.hint":      {"Run the installer again: it enables quota with one reboot, or says why it cannot.", "Führ den Installer erneut aus: Er schaltet Quota mit einem Neustart ein oder sagt, warum das nicht geht."},

	// system.proc
	"proc.hidden":    {"/proc hides other users' processes", "/proc verbirgt die Prozesse anderer Benutzer"},
	"proc.visible":   {"/proc shows every process to every user", "/proc zeigt jedem Benutzer alle Prozesse"},
	"proc.unmounted": {"/proc is not mounted", "/proc ist nicht eingehängt"},

	// system.pool.volume
	"pool.unavailable":      {"pool {{name}} ({{path}}) is not available: the volume is not mounted or its marker is missing", "Pool {{name}} ({{path}}) ist nicht verfügbar: Das Volume ist nicht eingehängt oder seine Markierung fehlt"},
	"pool.unavailable.hint": {"Mount the volume (mount {{path}}) and restart hostashi-agent. Until then its sites do not start and cannot be changed.", "Häng das Volume ein (mount {{path}}) und starte hostashi-agent neu. Bis dahin starten seine Sites nicht und lassen sich nicht ändern."},
	"pool.available":        {"pool {{name}} ({{path}}) is available", "Pool {{name}} ({{path}}) ist verfügbar"},
	"pool.error":            {"pool {{name}} ({{path}}): {{error}}", "Pool {{name}} ({{path}}): {{error}}"},
	"pool.nosuid":           {"pool {{name}} ({{path}}) is mounted without nosuid,nodev", "Pool {{name}} ({{path}}) ist ohne nosuid,nodev eingehängt"},
	"pool.nofstab":          {"pool {{name}} ({{path}}) has no entry in /etc/fstab", "Pool {{name}} ({{path}}) hat keinen Eintrag in /etc/fstab"},
	"pool.nosuid.nofstab":   {"pool {{name}} ({{path}}) is mounted without nosuid,nodev and has no entry in /etc/fstab", "Pool {{name}} ({{path}}) ist ohne nosuid,nodev eingehängt und hat keinen Eintrag in /etc/fstab"},
	"pool.mount.hint":       {"Mount it from /etc/fstab with nosuid,nodev,nofail, so nothing on the volume can raise privileges and its sites wait for it at boot.", "Häng ihn über /etc/fstab mit nosuid,nodev,nofail ein. Dann kann nichts auf dem Volume Rechte erweitern, und seine Sites warten beim Start darauf."},
	"pool.mounted":          {"pool {{name}} ({{path}}) is mounted with its marker, nosuid,nodev and an fstab entry", "Pool {{name}} ({{path}}) ist mit Markierung, nosuid,nodev und fstab-Eintrag eingehängt"},

	// system.pool
	"pool.mode.ok":   {"{{path}} is root 0711", "{{path}} gehört root mit 0711"},
	"pool.mode":      {"{{path}} has mode {{mode}}", "{{path}} hat die Rechte {{mode}}"},
	"pool.mode.hint": {"Site users can list the other sites' homes. Run the installer again (step {{step}}).", "Site-Benutzer können die Home-Verzeichnisse der anderen Sites auflisten. Führ den Installer erneut aus (Schritt {{step}})."},

	// service.panel
	"panel.hint":    {"Check the panel: systemctl status hostashi-panel, journalctl -u hostashi-panel.", "Prüf das Panel: systemctl status hostashi-panel, journalctl -u hostashi-panel."},
	"panel.error":   {"panel: {{error}}", "Panel: {{error}}"},
	"panel.down":    {"the panel does not answer on {{path}}: {{error}}", "das Panel antwortet nicht auf {{path}}: {{error}}"},
	"panel.problem": {"the panel reports a problem (HTTP {{code}}, agent {{agent}}, database {{db}})", "das Panel meldet ein Problem (HTTP {{code}}, Agent {{agent}}, Datenbank {{db}})"},
	"panel.running": {"the panel is running, version {{version}}", "das Panel läuft, Version {{version}}"},

	// site.disk
	"usage":           {"{{what}}: {{used}} of {{limit}} ({{percent}} %)", "{{what}}: {{used}} von {{limit}} ({{percent}} %)"},
	"usage.unlimited": {"{{what}}: {{used}}, unlimited", "{{what}}: {{used}}, unbegrenzt"},
	"usage.fail.hint": {"Almost full: writes fail at the limit. Raise the limit or free space.", "Fast voll: Am Limit schlagen Schreibvorgänge fehl. Erhöhe das Limit oder schaff Platz."},
	"usage.warn.hint": {"Filling up. Raise the limit or free space.", "Füllt sich. Erhöhe das Limit oder schaff Platz."},

	// site.database
	"db.usage":      {"databases: {{used}} of {{limit}} ({{percent}} %)", "Datenbanken: {{used}} von {{limit}} ({{percent}} %)"},
	"db.unlimited":  {"databases: {{used}}, no threshold", "Datenbanken: {{used}}, keine Warnschwelle"},
	"db.usage.hint": {"The databases keep growing past the threshold and fill the server's disk. Clean up old data (logs, sessions, revisions) or raise the threshold.", "Die Datenbanken wachsen über die Warnschwelle hinaus weiter und füllen die Platte des Servers. Räum alte Daten auf (Logs, Sessions, Revisionen) oder erhöhe die Schwelle."},

	// site.tls
	"cert.valid":   {"certificate valid until {{date}} ({{days}} days)", "Zertifikat gültig bis {{date}} ({{days}} Tage)"},
	"cert.expired": {"certificate expired on {{date}}", "Zertifikat am {{date}} abgelaufen"},
	"cert.hint":    {"For Let's Encrypt the renewal did not work: check the site's domains in the panel and the panel's log (journalctl -u hostashi-panel). An own certificate is never renewed automatically: upload a new one.", "Bei Let's Encrypt hat die Erneuerung nicht geklappt: Prüf die Domains der Site im Panel und das Log des Panels (journalctl -u hostashi-panel). Ein eigenes Zertifikat wird nie automatisch erneuert: Lade ein neues hoch."},

	// site.php
	"php.unknown":      {"PHP {{version}} has no known support dates", "Für PHP {{version}} sind keine Support-Daten bekannt"},
	"php.unknown.hint": {"Move the site to a supported PHP version.", "Stell die Site auf eine unterstützte PHP-Version um."},
	"php.ended":        {"PHP {{version}} gets no security fixes since {{until}}", "PHP {{version}} bekommt seit {{until}} keine Sicherheitsupdates mehr"},
	"php.ended.hint":   {"Move the site to a supported PHP version in the panel. Hostashi keeps it running and never switches by itself.", "Stell die Site im Panel auf eine unterstützte PHP-Version um. Hostashi lässt sie weiterlaufen und stellt nie selbst um."},
	"php.soon":         {"PHP {{version}} gets security fixes only until {{until}} ({{days}} days)", "PHP {{version}} bekommt nur noch bis {{until}} Sicherheitsupdates ({{days}} Tage)"},
	"php.soon.hint":    {"Plan the move to a newer PHP version.", "Plane den Umstieg auf eine neuere PHP-Version."},
	"php.supported":    {"PHP {{version}} gets security fixes until {{until}}", "PHP {{version}} bekommt Sicherheitsupdates bis {{until}}"},

	// Sockets of services (agent/doctor.go).
	"fail2ban.hint": {"Logins are not protected against brute force. Check: systemctl status fail2ban.", "Anmeldungen sind nicht gegen Brute Force geschützt. Prüf: systemctl status fail2ban."},
	"dbtool.hint":   {"Databases cannot be opened in Adminer. Check: systemctl status hostashi-adminer.", "Datenbanken lassen sich nicht in Adminer öffnen. Prüf: systemctl status hostashi-adminer."},

	// system.firewall
	"fw.off":             {"Hostashi's firewall is off (--no-firewall)", "Die Firewall von Hostashi ist aus (--no-firewall)"},
	"fw.off.hint":        {"Make sure a firewall in front of the server lets through only SSH, 80, 443 and the panel port.", "Sorg dafür, dass eine Firewall vor dem Server nur SSH, 80, 443 und den Port des Panels durchlässt."},
	"fw.unit.hint":       {"Check: systemctl status {{unit}}, journalctl -u {{unit}}. Run the installer again (step {{step}}).", "Prüf: systemctl status {{unit}}, journalctl -u {{unit}}. Führ den Installer erneut aus (Schritt {{step}})."},
	"fw.disabled":        {"the firewall is not enabled and is gone after a reboot", "die Firewall ist nicht aktiviert und nach einem Neustart weg"},
	"fw.state":           {"cannot ask systemd for the firewall's state", "systemd kann nicht nach dem Zustand der Firewall gefragt werden"},
	"fw.state.error":     {"cannot ask systemd for the firewall's state: {{error}}", "systemd kann nicht nach dem Zustand der Firewall gefragt werden: {{error}}"},
	"fw.inactive":        {"the firewall unit is not active, so its rules may not be loaded", "die Firewall-Unit ist nicht aktiv, ihre Regeln sind vielleicht nicht geladen"},
	"fw.noport":          {"{{path}} opens no TCP port", "{{path}} öffnet keinen TCP-Port"},
	"fw.ssh.closed":      {"sshd is configured for port {{ports}}, which the firewall does not open", "sshd ist für Port {{ports}} eingerichtet, den die Firewall nicht öffnet"},
	"fw.ssh.closed.hint": {"Run the installer again (step firewall) before you log out, or the next reload locks you out.", "Führ den Installer erneut aus (Schritt firewall), bevor du dich abmeldest. Sonst sperrt dich das nächste Neuladen aus."},
	"fw.on":              {"the firewall is on, open: TCP {{tcp}}", "die Firewall ist an, offen: TCP {{tcp}}"},
	"fw.on.udp":          {"the firewall is on, open: TCP {{tcp}}; UDP {{udp}}", "die Firewall ist an, offen: TCP {{tcp}}; UDP {{udp}}"},

	// system.updates
	"updates.off":        {"security updates are not installed automatically", "Sicherheitsupdates werden nicht automatisch installiert"},
	"updates.timer":      {"{{timer}} is not enabled, so no updates run", "{{timer}} ist nicht aktiviert, also laufen keine Updates"},
	"updates.norun":      {"security updates are installed every day (no run yet)", "Sicherheitsupdates werden täglich installiert (noch kein Lauf)"},
	"updates.stale":      {"the last automatic update ran on {{date}}", "das letzte automatische Update lief am {{date}}"},
	"updates.stale.hint": {"Check: systemctl status apt-daily-upgrade.timer, /var/log/unattended-upgrades/.", "Prüf: systemctl status apt-daily-upgrade.timer, /var/log/unattended-upgrades/."},
	"updates.ok":         {"security updates are installed every day, last run {{time}} UTC", "Sicherheitsupdates werden täglich installiert, letzter Lauf {{time}} UTC"},

	// system.reboot
	"reboot.no":              {"no reboot needed", "kein Neustart nötig"},
	"reboot.needed":          {"a reboot is needed to finish updates", "ein Neustart ist nötig, um Updates abzuschließen"},
	"reboot.needed.packages": {"a reboot is needed to finish updates ({{packages}})", "ein Neustart ist nötig, um Updates abzuschließen ({{packages}})"},
	"reboot.hint":            {"Reboot when it suits you, on the System page or with hostashi system reboot: the server never reboots by itself.", "Starte neu, wann es dir passt, auf der Seite System oder mit hostashi system reboot. Der Server startet nie von selbst neu."},

	// system.php
	"php.current":    {"every site's PHP runs the installed version", "PHP läuft bei jeder Site in der installierten Version"},
	"php.stale":      {"PHP of {{sites}} still runs a replaced binary after an update", "PHP von {{sites}} läuft nach einem Update noch mit einer ersetzten Datei"},
	"php.stale.hint": {"It misses the update's fixes. Restart it: systemctl restart {{unit}}.", "Ihm fehlen die Korrekturen des Updates. Starte es neu: systemctl restart {{unit}}."},

	// system.ssh
	"ssh.root":      {"root may log in over SSH with a password", "root darf sich per SSH mit Passwort anmelden"},
	"ssh.password":  {"SSH accepts passwords for the admin's login", "SSH nimmt Passwörter für die Anmeldung des Admins an"},
	"ssh.keys":      {"SSH logins outside the sites need a key", "SSH-Anmeldungen außerhalb der Sites brauchen einen Schlüssel"},
	"ssh.hint":      {"Log in with a key first, then set PasswordAuthentication no and KbdInteractiveAuthentication no in a file in /etc/ssh/sshd_config.d/ and run systemctl reload ssh.", "Melde dich zuerst mit einem Schlüssel an. Setz dann PasswordAuthentication no und KbdInteractiveAuthentication no in einer Datei in /etc/ssh/sshd_config.d/ und führ systemctl reload ssh aus."},
	"ssh.root.hint": {"Set PermitRootLogin prohibit-password. Log in with a key first, then set PasswordAuthentication no and KbdInteractiveAuthentication no in a file in /etc/ssh/sshd_config.d/ and run systemctl reload ssh.", "Setz PermitRootLogin prohibit-password. Melde dich zuerst mit einem Schlüssel an. Setz dann PasswordAuthentication no und KbdInteractiveAuthentication no in einer Datei in /etc/ssh/sshd_config.d/ und führ systemctl reload ssh aus."},

	// system.sysctl
	"sysctl.missing":    {"the kernel settings of the baseline are missing", "die Kernel-Einstellungen der Grundkonfiguration fehlen"},
	"sysctl.drift":      {"kernel settings differ from the baseline: {{drift}}", "Kernel-Einstellungen weichen von der Grundkonfiguration ab: {{drift}}"},
	"sysctl.drift.hint": {"A file in /etc/sysctl.d/ that sorts later may set them. Otherwise run the installer again (step {{step}}).", "Vielleicht setzt sie eine Datei in /etc/sysctl.d/, die später einsortiert ist. Sonst führ den Installer erneut aus (Schritt {{step}})."},
	"sysctl.ok":         {"{{count}} kernel settings match the baseline", "{{count}} Kernel-Einstellungen entsprechen der Grundkonfiguration"},

	// system.firewall.panel
	"fwpanel.error":        {"cannot read the panel's firewall rules: {{error}}", "die Firewall-Regeln des Panels können nicht gelesen werden: {{error}}"},
	"fwpanel.pending":      {"a change waits for its confirmation and rolls back in {{left}}", "eine Änderung wartet auf ihre Bestätigung und wird in {{left}} zurückgenommen"},
	"fwpanel.pending.hint": {"Confirm it in the browser that made it, or let it roll back.", "Bestätige sie im Browser, in dem sie gemacht wurde, oder lass sie zurücknehmen."},
	"fwpanel.none":         {"no rules from the panel: SSH and the panel are open to everyone", "keine Regeln aus dem Panel: SSH und das Panel sind für alle offen"},
	"fwpanel.rules":        {"rules from the panel: {{rules}}", "Regeln aus dem Panel: {{rules}}"},

	// hostashi.update, hostashi.apt
	"update.error":          {"cannot read the package state: {{error}}", "der Paketzustand kann nicht gelesen werden: {{error}}"},
	"update.error.hint":     {"Check /var/lib/dpkg/status and /var/lib/apt/lists.", "Prüf /var/lib/dpkg/status und /var/lib/apt/lists."},
	"apt.norepo":            {"the Hostashi apt repository is not configured", "das apt-Repository von Hostashi ist nicht eingerichtet"},
	"apt.norepo.hint":       {"Run the install script again, or restore /etc/apt/sources.list.d/hostashi.sources.", "Führ das Installationsskript erneut aus oder stell /etc/apt/sources.list.d/hostashi.sources wieder her."},
	"apt.nolist":            {"apt has no package list of {{repo}}", "apt hat keine Paketliste von {{repo}}"},
	"apt.nolist.hint":       {"Run: sudo apt update", "Führ aus: sudo apt update"},
	"apt.stale":             {"apt last fetched the package list of {{repo}} {{days}} days ago", "apt hat die Paketliste von {{repo}} zuletzt vor {{days}} Tagen geholt"},
	"apt.stale.hint":        {"Check that apt-daily.timer runs and that the server reaches {{repo}}: sudo apt update", "Prüf, ob apt-daily.timer läuft und der Server {{repo}} erreicht: sudo apt update"},
	"apt.ok":                {"apt fetched the package list of {{repo}} on {{time}} UTC", "apt hat die Paketliste von {{repo}} am {{time}} UTC geholt"},
	"update.newest":         {"hostashi {{installed}} is the newest version", "Hostashi {{installed}} ist die neueste Version"},
	"update.auto":           {"hostashi {{available}} is available (installed: {{installed}}); the daily automatic updates install it", "Hostashi {{available}} ist verfügbar (installiert: {{installed}}), die täglichen automatischen Updates installieren es"},
	"update.available":      {"hostashi {{available}} is available (installed: {{installed}})", "Hostashi {{available}} ist verfügbar (installiert: {{installed}})"},
	"update.available.hint": {"Install it with: sudo apt update && sudo apt upgrade. The update keeps a copy of the panel database first.", "Installiere es mit: sudo apt update && sudo apt upgrade. Das Update sichert vorher die Panel-Datenbank."},

	// hostashi.runtimes
	"runtimes.missing":      {"the package hostashi-runtimes is not installed: newer patch releases of the pinned Node.js and Python lines do not arrive", "das Paket hostashi-runtimes ist nicht installiert: Neuere Patch-Releases der festgelegten Node.js- und Python-Linien kommen nicht an"},
	"runtimes.missing.hint": {"Install it now: sudo systemctl start hostashi-runtimes-install.service (the daily hostashi-runtimes-install.timer does the same; errors: journalctl -u hostashi-runtimes-install).", "Installiere es jetzt: sudo systemctl start hostashi-runtimes-install.service (der tägliche hostashi-runtimes-install.timer macht dasselbe; Fehler: journalctl -u hostashi-runtimes-install)."},
	"runtimes.manual":       {"unattended-upgrades does not take the suite runtimes: hostashi-runtimes {{version}} stays as it is", "unattended-upgrades berücksichtigt die Suite runtimes nicht: hostashi-runtimes {{version}} bleibt, wie es ist"},
	"runtimes.manual.hint":  {"Run the installer again: sudo /usr/share/hostashi/install.sh upgrade", "Führ den Installer erneut aus: sudo /usr/share/hostashi/install.sh upgrade"},
	"runtimes.ok":           {"hostashi-runtimes {{version}} is installed and updated by unattended-upgrades", "hostashi-runtimes {{version}} ist installiert und wird von unattended-upgrades aktualisiert"},

	// system.files
	"files.units":      {"the file manager's units are missing ({{path}})", "die Units des Dateimanagers fehlen ({{path}})"},
	"files.units.hint": {"The Files tab cannot start. Run the installer again (step {{step}}).", "Der Tab Dateien kann nicht starten. Führ den Installer erneut aus (Schritt {{step}})."},
	"files.root":       {"the file manager of {{sites}} runs as root", "der Dateimanager von {{sites}} läuft als root"},
	"files.root.hint":  {"It must run as the site user. Stop it (systemctl stop hostashi-files@{{site}}.service) and check the unit: systemctl cat hostashi-files@{{site}}.service.", "Er muss als Site-Benutzer laufen. Stopp ihn (systemctl stop hostashi-files@{{site}}.service) und prüf die Unit: systemctl cat hostashi-files@{{site}}.service."},
	"files.old":        {"the file manager of {{sites}} has run for more than a day", "der Dateimanager von {{sites}} läuft seit mehr als einem Tag"},
	"files.old.hint":   {"It should stop 5 minutes after the last request. Check: systemctl status hostashi-files@{{site}}.service; stop it with systemctl stop hostashi-files@{{site}}.service.", "Er sollte 5 Minuten nach der letzten Anfrage stoppen. Prüf: systemctl status hostashi-files@{{site}}.service; stopp ihn mit systemctl stop hostashi-files@{{site}}.service."},
	"files.ok":         {"the file manager's units are installed; its helpers run as the site users", "die Units des Dateimanagers sind installiert, seine Helfer laufen als Site-Benutzer"},

	// system.fstab
	"fstab.none":        {"{{path}} does not exist", "{{path}} existiert nicht"},
	"fstab.missing":     {"{{path}} names a device that does not exist: {{devices}}", "{{path}} nennt ein Gerät, das es nicht gibt: {{devices}}"},
	"fstab.wait.hint":   {"Without nofail, the next boot stops in emergency mode, and logins wait 90 seconds for the device. Correct the line or comment it out, then run systemctl daemon-reload. Before formatting a volume, check with lsblk -f and /etc/fstab that nothing uses it.", "Ohne nofail bleibt der nächste Start im Emergency Mode stehen, und Anmeldungen warten 90 Sekunden auf das Gerät. Korrigier die Zeile oder kommentier sie aus und führ dann systemctl daemon-reload aus. Prüf vor dem Formatieren eines Volumes mit lsblk -f und /etc/fstab, dass nichts es nutzt."},
	"fstab.nofail.hint": {"The entry has nofail or noauto, so the boot goes on, but anything below the mount point now lands on the root disk. Correct the line or comment it out, then run systemctl daemon-reload. Before formatting a volume, check with lsblk -f and /etc/fstab that nothing uses it.", "Der Eintrag hat nofail oder noauto, der Start läuft also weiter. Alles unterhalb des Mount-Punkts landet jetzt aber auf der Systemplatte. Korrigier die Zeile oder kommentier sie aus und führ dann systemctl daemon-reload aus. Prüf vor dem Formatieren eines Volumes mit lsblk -f und /etc/fstab, dass nichts es nutzt."},
	"fstab.ok":          {"every device in {{path}} exists", "jedes Gerät in {{path}} existiert"},

	// system.node
	"node.behind":      {"Node.js {{line}} runs {{version}}, Hostashi brings {{release}}: the automatic update did not finish", "Node.js {{line}} läuft mit {{version}}, Hostashi bringt {{release}} mit: Das automatische Update ist nicht fertig geworden"},
	"node.behind.hint": {"Install the update under Node.js versions in the panel; its log shows what failed.", "Installiere das Update im Panel unter Node.js-Versionen. Sein Log zeigt, was fehlgeschlagen ist."},
	"node.ended":       {"Node.js {{line}} gets no fixes since {{end}} ({{sites}})", "Node.js {{line}} bekommt seit {{end}} keine Korrekturen mehr ({{sites}})"},
	"node.ended.hint":  {"Move the sites to a newer Node.js version on their Web tab.", "Stell die Sites in ihrem Tab Web auf eine neuere Node.js-Version um."},
	"node.soon":        {"Node.js {{line}} gets fixes only until {{end}} ({{days}} days; {{sites}})", "Node.js {{line}} bekommt nur noch bis {{end}} Korrekturen ({{days}} Tage; {{sites}})"},
	"node.soon.hint":   {"Plan the move of the sites to a newer Node.js version.", "Plane den Umstieg der Sites auf eine neuere Node.js-Version."},
	"node.ok":          {"every installed Node.js line runs the current release", "jede installierte Node.js-Linie läuft mit dem aktuellen Release"},

	// service.fail2ban.jails
	"jails.hint":  {"Check the jails: fail2ban-client status, and a local file under /etc/fail2ban/jail.d/ that may break them.", "Prüf die Jails: fail2ban-client status, und ob eine lokale Datei unter /etc/fail2ban/jail.d/ sie stört."},
	"jails.error": {"the jails cannot be read: {{error}}", "die Jails können nicht gelesen werden: {{error}}"},
	"jails.down":  {"not running: {{jails}}", "laufen nicht: {{jails}}"},
	"jails.ok":    {"all jails are running", "alle Jails laufen"},

	// service.nginx (internal/agent)
	"nginx.missing":     {"nginx is not set up", "nginx ist nicht eingerichtet"},
	"nginx.busy":        {"the configuration is being changed right now", "die Konfiguration wird gerade geändert"},
	"nginx.busy.hint":   {"Run hostashi doctor again in a moment.", "Führ hostashi doctor gleich noch einmal aus."},
	"nginx.error":       {"cannot read the web state: {{error}}", "der Web-Zustand kann nicht gelesen werden: {{error}}"},
	"nginx.none":        {"no configuration has been applied yet", "es wurde noch keine Konfiguration angewendet"},
	"nginx.none.hint":   {"Run hostashi web sync, or save a site in the panel.", "Führ hostashi web sync aus oder speichere eine Site im Panel."},
	"nginx.down":        {"nginx does not answer: {{error}}", "nginx antwortet nicht: {{error}}"},
	"nginx.down.hint":   {"Check: systemctl status nginx, journalctl -u nginx.", "Prüf: systemctl status nginx, journalctl -u nginx."},
	"nginx.behind":      {"nginx serves generation {{live}}, the current one is {{current}}", "nginx liefert Generation {{live}} aus, aktuell ist {{current}}"},
	"nginx.behind.hint": {"nginx did not load the current configuration. Run hostashi web sync.", "nginx hat die aktuelle Konfiguration nicht geladen. Führ hostashi web sync aus."},
	"nginx.drift":       {"files of the configuration were changed by hand: {{files}}", "Dateien der Konfiguration wurden von Hand geändert: {{files}}"},
	"nginx.drift.hint":  {"The next change replaces them. Use the panel's nginx snippets for your own settings.", "Die nächste Änderung ersetzt sie. Nimm für eigene Einstellungen die nginx-Snippets im Panel."},
	"nginx.unconfirmed": {"generation {{current}} was never confirmed", "Generation {{current}} wurde nie bestätigt"},
	"websync.hint":      {"Run hostashi web sync.", "Führ hostashi web sync aus."},
	"nginx.ok":          {"nginx serves generation {{live}}", "nginx liefert Generation {{live}} aus"},

	// service.mariadb (internal/agent)
	"mariadb.missing":      {"MariaDB is not set up", "MariaDB ist nicht eingerichtet"},
	"mariadb.missing.hint": {"Sites cannot have databases. Run the installer again.", "Sites können keine Datenbanken haben. Führ den Installer erneut aus."},
	"mariadb.down":         {"MariaDB does not answer: {{error}}", "MariaDB antwortet nicht: {{error}}"},
	"mariadb.down.hint":    {"Check: systemctl status mariadb.", "Prüf: systemctl status mariadb."},

	// site.* (internal/agent)
	"home.nopool":     {"the site's pool is not registered", "der Pool der Site ist nicht registriert"},
	"sitedisk.error":  {"cannot read the disk use: {{error}}", "die Belegung kann nicht gelesen werden: {{error}}"},
	"slice.error":     {"cannot read the slice: {{error}}", "der Slice kann nicht gelesen werden: {{error}}"},
	"slice.none":      {"no CPU, memory and process limits", "keine Limits für CPU, Arbeitsspeicher und Prozesse"},
	"slice.none.hint": {"Save the site's resource limits in the panel.", "Speichere die Ressourcenlimits der Site im Panel."},
	"slice.ok":        {"CPU, memory and process limits are set", "Limits für CPU, Arbeitsspeicher und Prozesse sind gesetzt"},
	"cert.error":      {"cannot read the certificate: {{error}}", "das Zertifikat kann nicht gelesen werden: {{error}}"},

	// service.valkey, site.cache (internal/agent)
	"valkey.missing":       {"Valkey is not installed", "Valkey ist nicht installiert"},
	"valkey.missing.hint":  {"Sites cannot have an object cache. Run the installer again.", "Sites können keinen Objekt-Cache haben. Führ den Installer erneut aus."},
	"valkey.shared":        {"the shared {{unit}} runs: every local user reaches it on 127.0.0.1:6379", "das gemeinsame {{unit}} läuft: Jeder lokale Benutzer erreicht es auf 127.0.0.1:6379"},
	"valkey.shared.hint":   {"Run: systemctl disable --now {{unit}} && systemctl mask {{unit}}", "Führ aus: systemctl disable --now {{unit}} && systemctl mask {{unit}}"},
	"valkey.unmasked":      {"{{unit}} is not masked, a package update could start it", "{{unit}} ist nicht maskiert, ein Paket-Update könnte es starten"},
	"valkey.unmasked.hint": {"Run the installer again, or: systemctl mask {{unit}}", "Führ den Installer erneut aus, oder: systemctl mask {{unit}}"},
	"valkey.ok":            {"Valkey is installed, the shared service is masked", "Valkey ist installiert, der gemeinsame Dienst ist maskiert"},
	"cache.error":          {"cannot read the object cache: {{error}}", "der Objekt-Cache kann nicht gelesen werden: {{error}}"},
	"cache.paused":         {"the object cache is stopped while the site is disabled", "der Objekt-Cache ist gestoppt, solange die Site deaktiviert ist"},
	"cache.down":           {"the object cache does not run ({{state}})", "der Objekt-Cache läuft nicht ({{state}})"},
	"cache.down.hint":      {"Check: journalctl -u hostashi-valkey@{{site}}.service. Turning the cache off and on again rewrites it.", "Prüf: journalctl -u hostashi-valkey@{{site}}.service. Wenn du den Cache aus- und wieder einschaltest, wird er neu geschrieben."},
	"cache.nodir":          {"the cache's socket directory is missing: {{error}}", "das Socket-Verzeichnis des Caches fehlt: {{error}}"},
	"cache.open":           {"{{path}} is reachable by other users ({{mode}})", "{{path}} ist für andere Benutzer erreichbar ({{mode}})"},
	"cache.open.hint":      {"Restart the cache: systemd creates the directory with mode 0700 for the site user.", "Starte den Cache neu: systemd legt das Verzeichnis mit Modus 0700 für den Site-Benutzer an."},
	"cache.ok":             {"the object cache runs, only the site reaches it", "der Objekt-Cache läuft, nur die Site erreicht ihn"},

	// system.pagecache, site.pagecache (internal/agent)
	"pagecache.ok":           {"the page cache directory is in order", "das Verzeichnis des Seiten-Caches ist in Ordnung"},
	"pagecache.error":        {"page cache directory: {{error}}", "Verzeichnis des Seiten-Caches: {{error}}"},
	"pagecache.error.hint":   {"Run hostashi web sync; it recreates the directory.", "Führ hostashi web sync aus, dann wird das Verzeichnis neu angelegt."},
	"pagecache.noquota":      {"cached pages do not count against the disk limit: the file system has no project quota", "zwischengespeicherte Seiten zählen nicht zum Speicherlimit: Das Dateisystem hat kein Project Quota"},
	"pagecache.noquota.hint": {"Only the cache size limits it.", "Nur die Cache-Größe begrenzt sie."},
	"pagecache.pool":         {"pool {{pool}}: {{error}}", "Pool {{pool}}: {{error}}"},
	"pagecache.orphan":       {"pool {{pool}} holds a page cache directory of {{site}}, which has no page cache", "Pool {{pool}} enthält ein Seiten-Cache-Verzeichnis von {{site}}, die keinen Seiten-Cache hat"},
	"pagecache.orphan.hint":  {"Run hostashi web sync; it removes the directory.", "Führ hostashi web sync aus, dann wird das Verzeichnis entfernt."},

	// panel.tls (internal/hosting)
	"paneltls.error":     {"cannot read the state of the panel's certificate", "der Zustand des Panel-Zertifikats kann nicht gelesen werden"},
	"paneltls.none":      {"no certificate for {{domain}}: {{error}}", "kein Zertifikat für {{domain}}: {{error}}"},
	"paneltls.expired":   {"certificate for {{domain}} expired on {{date}}", "Zertifikat für {{domain}} am {{date}} abgelaufen"},
	"paneltls.valid":     {"certificate for {{domain}} valid until {{date}} ({{days}} days)", "Zertifikat für {{domain}} gültig bis {{date}} ({{days}} Tage)"},
	"paneltls.renewfail": {"certificate for {{domain}} valid until {{date}} ({{days}} days); the last renewal failed: {{error}}", "Zertifikat für {{domain}} gültig bis {{date}} ({{days}} Tage); die letzte Erneuerung ist fehlgeschlagen: {{error}}"},
	"paneltls.hint":      {"See System → Panel certificate. Let's Encrypt needs the panel's host name to point at this server; an own certificate is never renewed automatically: upload a new one.", "Siehe System → Panel-Zertifikat. Für Let's Encrypt muss der Hostname des Panels auf diesen Server zeigen; ein eigenes Zertifikat wird nie automatisch erneuert: Lade ein neues hoch."},

	// panel.malware, site.malware (internal/hosting)
	"clamav.error":          {"cannot read the state of ClamAV", "der Zustand von ClamAV kann nicht gelesen werden"},
	"agentlog.hint":         {"Check the agent's log (journalctl -u hostashi-agent).", "Prüf das Log des Agents (journalctl -u hostashi-agent)."},
	"clamav.failed":         {"ClamAV could not be installed or removed", "ClamAV konnte nicht installiert oder entfernt werden"},
	"clamav.failed.hint":    {"See the install log under System → Virus scan and switch it on or off again.", "Sieh dir das Installationslog unter System → Virenscan an und schalte ihn erneut ein oder aus."},
	"realtime.failed":       {"the real-time scan could not be switched", "der Echtzeit-Scan konnte nicht umgeschaltet werden"},
	"realtime.failed.hint":  {"See the install log under System → Virus scan and switch the real-time scan on or off again.", "Sieh dir das Installationslog unter System → Virenscan an und schalte den Echtzeit-Scan erneut ein oder aus."},
	"clamav.nosigs":         {"{{engine}} has no signatures yet", "{{engine}} hat noch keine Signaturen"},
	"clamav.nosigs.hint":    {"freshclam downloads them within minutes of the install. If this stays, check journalctl -u clamav-freshclam.", "freshclam lädt sie wenige Minuten nach der Installation herunter. Bleibt es so, prüf journalctl -u clamav-freshclam."},
	"clamav.sigage":         {"{{engine}}: cannot read the age of the signatures", "{{engine}}: Das Alter der Signaturen kann nicht gelesen werden"},
	"clamav.old":            {"{{engine}}: the signatures are {{days}} days old", "{{engine}}: Die Signaturen sind {{days}} Tage alt"},
	"clamav.old.hint":       {"freshclam has not updated them. Check journalctl -u clamav-freshclam; a distribution that stopped updating ClamAV makes the downloads fail.", "freshclam hat sie nicht aktualisiert. Prüf journalctl -u clamav-freshclam; eine Distribution, die ClamAV nicht mehr aktualisiert, lässt die Downloads fehlschlagen."},
	"clamav.ok":             {"{{engine}}, signatures from {{time}} UTC", "{{engine}}, Signaturen vom {{time}} UTC"},
	"malware.finding":       {"1 open virus scan finding", "1 offener Fund des Virenscans"},
	"malware.findings":      {"{{count}} open virus scan findings", "{{count}} offene Funde des Virenscans"},
	"malware.findings.hint": {"Open the site's Virus scan tab: quarantine the files, or mark them as false positives.", "Öffne den Tab Virenscan der Site: Verschieb die Dateien in die Quarantäne oder markier sie als Fehlalarm."},

	// dbserver.* (internal/hosting)
	"dbserver.unchecked":      {"database server {{name}} was not checked yet", "Datenbankserver {{name}} wurde noch nicht geprüft"},
	"dbserver.unchecked.hint": {"Check it under Databases, Servers.", "Prüf ihn unter Datenbanken, Server."},
	"dbserver.down":           {"database server {{name}} is not reachable: {{error}}", "Datenbankserver {{name}} ist nicht erreichbar: {{error}}"},
	"dbserver.down.hint":      {"Sites with databases there fail, and their backups are incomplete. Check the server, the network and the admin user.", "Sites mit Datenbanken dort schlagen fehl, und ihre Backups sind unvollständig. Prüf den Server, das Netzwerk und den Admin-Benutzer."},
	"dbserver.ok":             {"database server {{name}} is reachable", "Datenbankserver {{name}} ist erreichbar"},
	"dbserver.stale":          {"database server {{name}} was last checked {{time}}", "Datenbankserver {{name}} wurde zuletzt {{time}} geprüft"},
	"dbserver.stale.hint":     {"The panel checks every 15 minutes; restart hostashi.service if this stays.", "Das Panel prüft alle 15 Minuten; starte hostashi.service neu, wenn es so bleibt."},
	"dbserver.notls":          {"database server {{name}} is reached without TLS: passwords and data cross the network in plain text", "Datenbankserver {{name}} wird ohne TLS erreicht: Passwörter und Daten gehen im Klartext durchs Netzwerk"},
	"dbserver.notls.hint":     {"Fine only in a private network you trust; otherwise turn on TLS on the server and add it again with TLS.", "Nur in Ordnung in einem privaten Netzwerk, dem du vertraust; sonst schalte TLS auf dem Server ein und füg ihn mit TLS neu hinzu."},
	"dbserver.cert":           {"the TLS certificate of database server {{name}} ends in {{days}} days", "das TLS-Zertifikat von Datenbankserver {{name}} läuft in {{days}} Tagen ab"},
	"dbserver.cert.fail.hint": {"Renew it on the database server; without it Hostashi cannot connect.", "Erneuer es auf dem Datenbankserver; ohne es kann Hostashi sich nicht verbinden."},
	"dbserver.cert.warn.hint": {"Renew it on the database server.", "Erneuer es auf dem Datenbankserver."},

	// panel.checks, panel.mail, panel.pgclient (internal/serve)
	"panelchecks.error":     {"some of the panel's own checks could not run", "einige Prüfungen des Panels konnten nicht laufen"},
	"panellog.hint":         {"Check the panel's log (journalctl -u hostashi-panel).", "Prüf das Log des Panels (journalctl -u hostashi-panel)."},
	"mail.settings.error":   {"cannot read the email settings", "die E-Mail-Einstellungen können nicht gelesen werden"},
	"mail.outbox.error":     {"cannot read the outbox", "der Postausgang kann nicht gelesen werden"},
	"mail.ok":               {"relay {{relay}}, no failed emails in the last 24 hours", "Relay {{relay}}, keine fehlgeschlagenen E-Mails in den letzten 24 Stunden"},
	"mail.failed.one":       {"1 email could not be sent in the last 24 hours", "1 E-Mail konnte in den letzten 24 Stunden nicht gesendet werden"},
	"mail.failed.one.last":  {"1 email could not be sent in the last 24 hours (last: {{error}})", "1 E-Mail konnte in den letzten 24 Stunden nicht gesendet werden (zuletzt: {{error}})"},
	"mail.failed":           {"{{count}} emails could not be sent in the last 24 hours", "{{count}} E-Mails konnten in den letzten 24 Stunden nicht gesendet werden"},
	"mail.failed.last":      {"{{count}} emails could not be sent in the last 24 hours (last: {{error}})", "{{count}} E-Mails konnten in den letzten 24 Stunden nicht gesendet werden (zuletzt: {{error}})"},
	"mail.failed.hint":      {"Check the settings on the Email page and send a test email. Links in invitations can still be copied on the Users page.", "Prüf die Einstellungen auf der Seite E-Mail und schick eine Test-E-Mail. Links in Einladungen kannst du weiterhin auf der Seite Benutzer kopieren."},
	"pgclient.error":        {"cannot read which PostgreSQL client is installed", "es kann nicht gelesen werden, welcher PostgreSQL-Client installiert ist"},
	"pgclient.error.hint":   {"The agent did not answer; check hostashi-agent.service.", "Der Agent hat nicht geantwortet; prüf hostashi-agent.service."},
	"pgclient.none":         {"no PostgreSQL client is installed for database server {{name}}: its backups, imports and exports fail", "für Datenbankserver {{name}} ist kein PostgreSQL-Client installiert: Seine Backups, Importe und Exporte schlagen fehl"},
	"pgclient.none.hint":    {"Install it with POST /api/v1/db-servers/postgresql-client; adding a PostgreSQL server starts it too.", "Installier ihn mit POST /api/v1/db-servers/postgresql-client; das Hinzufügen eines PostgreSQL-Servers startet das auch."},
	"pgclient.old":          {"the PostgreSQL client {{client}} is older than database server {{name}} (PostgreSQL {{version}}): its backups, imports and exports fail", "der PostgreSQL-Client {{client}} ist älter als Datenbankserver {{name}} (PostgreSQL {{version}}): Seine Backups, Importe und Exporte schlagen fehl"},
	"pgclient.update.hint":  {"Update Hostashi: a newer release brings the client for PostgreSQL {{server}}.", "Aktualisier Hostashi: Eine neuere Version bringt den Client für PostgreSQL {{server}} mit."},
	"pgclient.install.hint": {"Install the PostgreSQL {{newest}} client with POST /api/v1/db-servers/postgresql-client.", "Installier den PostgreSQL-{{newest}}-Client mit POST /api/v1/db-servers/postgresql-client."},
	"pgclient.ok":           {"the PostgreSQL client {{client}} can dump database server {{name}} (PostgreSQL {{version}})", "der PostgreSQL-Client {{client}} kann Datenbankserver {{name}} sichern (PostgreSQL {{version}})"},
}
