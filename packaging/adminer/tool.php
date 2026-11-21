<?php
// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Adminer as Hostashi runs it (ADR-0038), through Adminer's extension
// interface: one login (the ticket's temporary user), one server (the local
// MariaDB socket or the ticket's external server), one database, no permanent login and nothing loaded from
// adminer.org. index.php checks the ticket and the session before this
// class is used.

namespace Hostashi;

class Tool extends \Adminer\Adminer
{
    public function name()
    {
        return "<a href='?username=" . rawurlencode(LOGIN['user']) . '&amp;db=' . rawurlencode(LOGIN['database'])
            . "' id='h1'>Database " . \Adminer\h(LOGIN['database']) . '</a>';
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
