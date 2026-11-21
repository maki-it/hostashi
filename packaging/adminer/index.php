<?php
// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Hostashi's front for Adminer (ADR-0038), installed next to the pinned
// adminer.php under /usr/share/hostashi/adminer. It is the only way in:
//
// - A session starts only with a one-time ticket that the Hostashi agent
//   wrote for one database. The ticket is read and removed on first use; the
//   password in it never reaches the browser (Adminer keeps it in the PHP
//   session, encrypted with a key in a cookie).
// - Adminer's own login form is gone, and its login accepts only the
//   ticket's temporary user, for the ticket's database, on the local
//   MariaDB socket or on the external server the ticket names.
// - The session ends at the ticket's expiry; the agent drops the user then.

namespace Hostashi {

const TICKET_DIR = '/run/hostashi-adminer/tickets';

/** Sends a short page without Adminer and stops. */
function stop(int $status, string $message): never
{
    http_response_code($status);
    header('Content-Type: text/html; charset=utf-8');
    header('Cache-Control: no-store');
    header("Content-Security-Policy: default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'");
    echo "<!DOCTYPE html>\n<html lang=\"en\"><meta charset=\"utf-8\"><meta name=\"robots\" content=\"noindex\">",
        "<title>Database tool</title><p>", htmlspecialchars($message, ENT_QUOTES), "</p></html>\n";
    exit;
}

/** Reads and removes a ticket; null if it is unknown, used or expired. */
function take_ticket(string $id): ?array
{
    if (!preg_match('/^[0-9a-f]{32}$/D', $id)) {
        return null;
    }

    $path = TICKET_DIR . '/' . $id;
    if (is_link($path)) {
        return null;
    }

    $raw = @file_get_contents($path, false, null, 0, 4096);
    // Only the request that removes the file may use it.
    if ($raw === false || !@unlink($path)) {
        return null;
    }

    $t = json_decode($raw, true);
    if (!is_array($t)) {
        return null;
    }

    foreach (['database', 'user', 'password', 'expires', 'ticketExpires'] as $k) {
        if (!isset($t[$k]) || !is_string($t[$k])) {
            return null;
        }
    }

    $expires = strtotime($t['expires']);
    $ticketExpires = strtotime($t['ticketExpires']);
    if ($expires === false || $ticketExpires === false || $ticketExpires < time() || $expires < time()) {
        return null;
    }

    if (!preg_match('/^hostashi_tool_[0-9a-f]{16}$/D', $t['user']) || !preg_match('/^[a-z][a-z0-9_]{0,63}$/D', $t['database'])) {
        return null;
    }

    $login = ['database' => $t['database'], 'user' => $t['user'], 'password' => $t['password'], 'expires' => $expires, 'server' => '', 'tls' => '', 'ca' => ''];

    // An external server (ADR-0041 addendum 1): the agent took host and
    // port from the panel's server list, the pool reaches only its
    // addresses.
    if (isset($t['host'])) {
        $server = remote_server($t);
        if ($server === null) {
            return null;
        }

        $login = array_merge($login, $server);
    }

    return $login;
}

/** The server, TLS mode and CA of an external ticket; null if malformed. */
function remote_server(array $t): ?array
{
    $host = $t['host'] ?? null;
    $port = $t['port'] ?? null;
    $tls = $t['tls'] ?? null;
    $ca = $t['caFile'] ?? '';

    if (!is_string($host) || !is_int($port) || $port < 1 || $port > 65535 || !in_array($tls, ['verify', 'verify-ca', 'none'], true) || !is_string($ca)) {
        return null;
    }

    $ip = filter_var($host, FILTER_VALIDATE_IP);
    if ($ip === false && !preg_match('/^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$/D', $host)) {
        return null;
    }

    if ($tls === 'verify-ca' ? !preg_match('~^/etc/hostashi-adminer/servers/ca-[0-9]+\.pem$~D', $ca) : $ca !== '') {
        return null;
    }

    $name = str_contains($host, ':') ? '[' . $host . ']' : $host;

    return ['server' => $name . ':' . $port, 'tls' => $tls, 'ca' => $ca];
}

/** The session's login, null outside a ticket session. */
function session_login(): ?array
{
    $s = $_SESSION['hostashi'] ?? null;

    return is_array($s) ? $s : null;
}

// Requests may only reach Adminer through this file.
if (basename($_SERVER['SCRIPT_FILENAME'] ?? '') !== 'index.php') {
    stop(404, 'Not found.');
}

// Adminer starts the session itself; the wrapper needs it first, with the
// same settings (the pool sets cookie_secure, httponly and samesite).
session_cache_limiter('');
session_name('hostashi_adminer');
session_start();

// Nobody logs in through Adminer's form or with a login of their own.
unset($_POST['auth'], $_GET['server'], $_GET['ext']);

$ticket = $_GET['ticket'] ?? null;
$ticketLogin = false;

if (is_string($ticket)) {
    $t = take_ticket($ticket);
    if ($t === null) {
        stop(403, 'This link is used up or expired. Open the database again from the Hostashi panel.');
    }

    // A ticket replaces whatever session this browser had.
    $_SESSION = [];
    session_regenerate_id(true);
    $_SESSION['hostashi'] = [
        'database' => $t['database'], 'user' => $t['user'], 'expires' => $t['expires'],
        'server' => $t['server'], 'tls' => $t['tls'], 'ca' => $t['ca'],
    ];

    $_POST['auth'] = [
        'driver' => 'server', 'server' => '', 'username' => $t['user'],
        'password' => $t['password'], 'db' => $t['database'], 'permanent' => '',
    ];
    $ticketLogin = true;

    // The ticket leaves the address; Adminer redirects to its own.
    unset($_GET['ticket']);
    $_SERVER['REQUEST_URI'] = '/';
    $_SERVER['REQUEST_METHOD'] = 'POST';
}

$login = session_login();

if ($login === null) {
    stop(403, 'Open a database from the Hostashi panel to use this tool.');
}

if ($login['expires'] < time()) {
    $_SESSION = [];
    session_destroy();
    stop(403, 'The session has ended. Open the database again from the Hostashi panel.');
}

// Only the ticket's user, database and the local server.
if (!$ticketLogin && (($_GET['username'] ?? null) !== $login['user'] || ($_GET['db'] ?? '') !== $login['database'])) {
    header('Location: /?username=' . rawurlencode($login['user']) . '&db=' . rawurlencode($login['database']), true, 303);
    exit;
}

define('Hostashi\TICKET_LOGIN', $ticketLogin);
define('Hostashi\LOGIN', $login);
}

namespace {
    // Adminer asks for this object while adminer.php loads; its class
    // extends Adminer's own and so is declared only then.
    function adminer_object()
    {
        require __DIR__ . '/tool.php';

        return new \Hostashi\Tool();
    }

    require __DIR__ . '/adminer.php';
}
