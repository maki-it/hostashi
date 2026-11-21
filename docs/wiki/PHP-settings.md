# PHP settings

Every site runs its own PHP. On the site's **PHP** tab you choose the PHP version and set the most common PHP settings, such as the memory limit, the largest upload and the time zone. Only the twelve settings below can be set. Settings that keep sites apart, such as `open_basedir` or `disable_functions`, stay fixed.

**For:** everyone who can change the site.

## Change a setting

1. Open the site and go to the **PHP** tab.
2. Under **PHP settings**, fill in the fields you want to change. An empty field uses the Hostashi default, shown in grey.
3. Click **Save PHP settings**. PHP and nginx use the new values right away.

**Reset all to defaults** empties every field. To reset one setting, empty its field (or choose *Default* in a list) and save.

## The settings

| Field | PHP setting | Range | Default |
|-------|-------------|-------|---------|
| Memory limit | `memory_limit` | 32 to 4096 MiB | 256 MiB |
| Largest upload | `upload_max_filesize` | 1 to 2048 MiB | 64 MiB |
| Largest request | `post_max_size` | 1 to 2048 MiB, at least the largest upload | 64 MiB |
| Execution time | `max_execution_time` | 10 to 3600 s | 60 s |
| Input time | `max_input_time` | 10 to 3600 s | 60 s |
| Input variables | `max_input_vars` | 1000 to 100000 | 3000 |
| Files per request | `max_file_uploads` | 1 to 500 | 20 |
| Time zone | `date.timezone` | a name such as `Europe/Berlin` | UTC |
| Error reporting | `error_reporting` | production, development, errors only | production |
| Show errors in pages | `display_errors` | on, off | off |
| Session lifetime | `session.gc_maxlifetime` | 60 s to 30 days | 1440 s |
| Short open tag | `short_open_tag` | on, off | off |

If you raise only the largest upload, the largest request follows it.

## What still limits a site

The values are defaults for the site. The site's own `.user.ini` file or `ini_set()` in its code can still change them, as on most hosts; many plugins rely on that. The server's limits stay in place:

- nginx takes no request larger than **Largest request** and waits for PHP no longer than **Execution time** plus 10 seconds (at least 60 seconds). A higher value in `.user.ini` does not get past nginx.
- The site's memory limit (on the **Limits** tab) caps all of its PHP processes together, whatever `memory_limit` says.

A server snippet on the **nginx** tab that sets `client_max_body_size` or `fastcgi_read_timeout` itself wins over these values.
