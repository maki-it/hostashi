# Security headers

A site can send the common security headers with every answer: error pages, files and the answers of PHP alike. You switch them on the site's **Security › Security headers** tab. HSTS has its own switch on the **Domains & HTTPS › HTTPS** tab.

**For:** everyone who can change the site.

## Change the headers

1. Open the site and go to the **Security › Security headers** tab.
2. Under **Security headers**, choose a value for each header, or *Off*.
3. Click **Save security headers**. nginx sends the new headers right away.

**Use recommended** switches on the three headers that new sites get, see below.

## The headers

| Field | Header | Choices |
|-------|--------|---------|
| MIME type sniffing | `X-Content-Type-Options` | off, `nosniff` |
| Show in frames | `X-Frame-Options` | off, `SAMEORIGIN` (only on this site), `DENY` (never) |
| Referrer | `Referrer-Policy` | off or one of the eight standard values |
| Browser features | `Permissions-Policy` | off, *Strict* (camera, microphone, location, payment and USB off for everyone), an own policy |
| Content Security Policy | `Content-Security-Policy` | off, report only, enforce; with an own policy |

New sites created in the panel or the API start with `nosniff`, `SAMEORIGIN` and `strict-origin-when-cross-origin`. Sites that existed before and sites imported from CloudPanel start with none, so nothing changes for them until you switch a header on.

## Content Security Policy

A policy that is too strict breaks a site at once: the browser blocks scripts, styles, fonts or images that the policy does not allow. Test a new policy with **Report only** first. The browser then only reports violations in its developer console. When the console stays quiet on the pages that matter, switch to **Enforce**.

Write the policy as directives separated by `;`, for example `default-src 'self'; img-src 'self' data:`. The panel refuses unknown directives, a directive given twice, and characters that have no place in a header, such as `"`, `\` or `$`.

## When the application sends the same header

A header switched on here replaces the header of the same name that the application sends itself, so the browser gets exactly one value. For example, WordPress sends `X-Frame-Options: SAMEORIGIN` on its login page; with **Show in frames** set to `DENY`, the page gets `DENY` only. A header that is *Off* here passes through as the application sends it.

## Custom nginx snippets

A custom snippet on the **Operations › nginx** tab must not set a header that is switched on here. The panel refuses saving such a snippet, and it refuses switching on a header that a snippet already sets. Remove the header from one of the two places first.

Headers that a snippet adds inside `location /` no longer drop the site's other headers, such as HSTS.
