<?php
// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Adminer as Hostashi runs it (ADR-0038), through Adminer's extension
// interface: one login (the ticket's temporary user), one server (the local
// MariaDB socket or the ticket's external server), one database, no permanent login and nothing loaded from
// adminer.org. index.php checks the ticket and the session before this
// class is used. adminer.css gives it the panel's look; the script in
// head() rebuilds the top bar like the panel's.

namespace Hostashi;

class Tool extends \Adminer\Adminer
{
    public function name()
    {
        return "<a href='?username=" . rawurlencode(LOGIN['user']) . '&amp;db=' . rawurlencode(LOGIN['database'])
            . "' id='h1'>Database " . \Adminer\h(LOGIN['database']) . '</a>';
    }

    // Hostashi's look, in the colour scheme the user has in the panel:
    // "light", "dark" or "" for the system's. The pool runs with chdir=/,
    // so the file is named here instead of found next to the script.
    public function css()
    {
        $v = @filemtime(__DIR__ . '/adminer.css');

        return ['adminer.css?v=' . ($v === false ? 0 : $v) => LOGIN['scheme']];
    }

    // The scheme on <html> for adminer.css, and the top bar in the panel's
    // words: "Hosting › Databases › <db> › …" instead of driver and server,
    // with the session's end, the way back and the logout in one row.
    public function head($dark = null)
    {
        $scheme = json_encode(LOGIN['scheme'], JSON_HEX_TAG | JSON_HEX_AMP);
        $panel = json_encode(LOGIN['panel'] === '' ? '' : LOGIN['panel'] . '/#/databases', JSON_HEX_TAG | JSON_HEX_AMP);
        $db = json_encode(LOGIN['database'], JSON_HEX_TAG | JSON_HEX_AMP);
        echo \Adminer\script(<<<JS
            if ($scheme) document.documentElement.dataset.scheme = $scheme;
            document.addEventListener('DOMContentLoaded', () => {
                const b = document.getElementById('breadcrumb');
                if (b) hostashiCrumbs(b, $panel, $db);
                const end = document.getElementById('hostashi-ends');
                if (!end) return;
                const tick = () => {
                    const m = Math.max(0, Math.ceil((end.dataset.expires * 1000 - Date.now()) / 60000));
                    end.replaceChildren();
                    if (m > 0) {
                        const w = document.createElement('span');
                        w.className = 'ends-word';
                        w.textContent = 'Session ends in ';
                        end.append(w, m + ' min');
                    } else {
                        end.textContent = 'Session ended';
                    }
                    end.classList.toggle('soon', m <= 5);
                };
                tick();
                setInterval(tick, 20000);
            });

            // Adminer's breadcrumb is "driver » server » db » … » page", as
            // links and text. Driver and server go; the panel's levels lead.
            function hostashiCrumbs(b, panel, db) {
                const crumbs = [{ label: 'Hosting' }, { label: 'Databases', href: panel || undefined }];
                let skip = 2;
                for (const n of [...b.childNodes]) {
                    if (n.nodeType === 1 && n.tagName === 'A') {
                        if (skip-- > 0) continue;
                        crumbs.push({ label: n.textContent, href: n.getAttribute('href') });
                    } else if (n.nodeType === 3) {
                        for (const part of n.textContent.split('»')) {
                            const t = part.trim();
                            if (t !== '') crumbs.push({ label: t === 'Database: ' + db ? db : t });
                        }
                    }
                }
                if (crumbs.length === 2) crumbs.push({ label: db });
                for (const n of [...b.childNodes]) {
                    if (n.nodeType === 3 || (n.nodeType === 1 && n.tagName === 'A')) n.remove();
                }

                const ol = document.createElement('ol');
                ol.className = 'crumbs';
                crumbs.forEach((c, i) => {
                    const last = i === crumbs.length - 1;
                    const li = document.createElement('li');
                    const e = document.createElement(c.href && !last ? 'a' : 'span');
                    e.textContent = c.label;
                    e.title = c.label;
                    if (c.href && !last) e.href = c.href;
                    if (last) e.setAttribute('aria-current', 'page');
                    li.append(e);
                    if (!last) {
                        const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
                        const path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
                        svg.setAttribute('viewBox', '0 0 256 256');
                        svg.setAttribute('aria-hidden', 'true');
                        path.setAttribute('d', 'M96 48l80 80-80 80');
                        path.setAttribute('fill', 'none');
                        path.setAttribute('stroke', 'currentColor');
                        path.setAttribute('stroke-width', '16');
                        path.setAttribute('stroke-linecap', 'round');
                        path.setAttribute('stroke-linejoin', 'round');
                        svg.append(path);
                        li.append(svg);
                    }
                    ol.append(li);
                });
                b.prepend(ol);

                const bar = document.getElementById('hostashi-bar');
                const logout = document.querySelector('.logout');
                if (bar) b.append(bar);
                if (logout) b.append(logout.closest('form'));
                b.classList.add('hostashi');

                // Short of room, "Hosting" goes first and "Databases" turns
                // into "…"; only then does adminer.css shorten the names.
                const items = [...ol.children];
                const cut = () => ol.scrollWidth > ol.clientWidth + 1 || items.some((li) => li.firstChild.scrollWidth > li.firstChild.clientWidth + 1);
                const fit = () => {
                    items[0].hidden = false;
                    items[1].firstChild.textContent = 'Databases';
                    if (cut()) items[0].hidden = true;
                    if (cut()) items[1].firstChild.textContent = '…';
                };
                fit();
                addEventListener('resize', fit);
            }
            JS);

        return true;
    }

    // The session's end and the way back to the panel, moved into the top
    // bar by the script in head().
    public function navigation($missing)
    {
        parent::navigation($missing);
        $left = max(0, (int) ceil((LOGIN['expires'] - time()) / 60));
        echo "<div id='hostashi-bar'><span id='hostashi-ends' data-expires='", (int) LOGIN['expires'], "'>",
            "<span class='ends-word'>Session ends in </span>$left min</span>";
        if (LOGIN['panel'] !== '') {
            // The panel extends the session (ADR-0038 addendum 1); the
            // tool itself has no way to.
            $back = \Adminer\h(LOGIN['panel'] . '/#/databases?db=' . rawurlencode(LOGIN['database']));
            echo "<a id='hostashi-extend' href='$back' target='_blank' rel='noopener' title='Extend this session in the panel'>+30 min</a>",
                "<a id='hostashi-panel' href='$back'>Back to panel</a>";
        }
        echo "</div>\n";
    }

    // The empty server is mysqli's default: localhost over the socket. An
    // external server comes from the ticket, never from the request.
    public function credentials()
    {
        return [LOGIN['server'] ?? '', LOGIN['user'], \Adminer\get_password()];
    }

    // TLS to an external server: the system CAs or the server's own CA,
    // always with the certificate and host name checked; nothing for the
    // local socket or a server without TLS.
    public function connectSsl()
    {
        switch (LOGIN['tls'] ?? '') {
            case 'verify':
                return ['key' => '', 'cert' => '', 'ca' => '/etc/ssl/certs/ca-certificates.crt', 'verify' => true];
            case 'verify-ca':
                return ['key' => '', 'cert' => '', 'ca' => LOGIN['ca'], 'verify' => true];
            default:
                return null;
        }
    }

    public function login($login, $password)
    {
        if ($login !== LOGIN['user'] || !is_string($password) || $password === '') {
            return 'Open the database from the Hostashi panel.';
        }

        return true;
    }

    // The ticket's login skips Adminer's form token: it does not come from
    // a form. Every other request with an auth field is removed before
    // Adminer runs (index.php).
    public function verifyLoginToken()
    {
        return !TICKET_LOGIN;
    }

    public function loginForm()
    {
        echo '<p>Open the database from the Hostashi panel.</p>';
    }

    public function permanentLogin($create = false)
    {
        return '';
    }

    public function database()
    {
        return LOGIN['database'];
    }

    public function databases($flush = true)
    {
        return [LOGIN['database']];
    }

    // No version check against adminer.org.
    public function verifyVersion()
    {
        return false;
    }

    // Adminer's policy without adminer.org, and never in a frame.
    public function csp(array $csp)
    {
        foreach ($csp as &$c) {
            $c['connect-src'] = "'self'";
            unset($c['frame-src']);
            $c['frame-ancestors'] = "'none'";
        }

        return $csp;
    }

    public function headers()
    {
        header('Referrer-Policy: no-referrer');
    }

    // Reading SQL files from the server's disk is off.
    public function importServerPath()
    {
        return '';
    }
}
