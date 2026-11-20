// SPDX-License-Identifier: AGPL-3.0-or-later

// Development-only gallery of the base components (gallery.html). It shows
// every component in the current scheme; ?scheme=dark or ?scheme=light sets one.

import { ArrowClockwise, Certificate, DownloadSimple, Moon, PencilSimple, Plus, Sun, Trash } from "@phosphor-icons/react";
import { StrictMode, useState } from "react";
import { createRoot } from "react-dom/client";
import { Badge, Button, Card, Field, IconButton, Notice, RowActions, Table, tabPanelProps, Tabs } from ".";
import type { Column, Sort } from ".";
import "./tokens.css";
import "./ui.css";
import "./gallery.css";

type Site = { name: string; domain: string; php: string; state: "ok" | "warn" | "off"; disk: number };

const sites: Site[] = [
  { name: "shop", domain: "shop.example.com", php: "8.4", state: "ok", disk: 3.2 },
  { name: "blog", domain: "blog.example.com", php: "8.5", state: "warn", disk: 12.8 },
  { name: "archive", domain: "old.example.com", php: "7.4", state: "off", disk: 0.4 },
];

const stateWord = { ok: "Running", warn: "Certificate expires", off: "Disabled" } as const;

const tabs = [
  { id: "overview", label: "Overview" },
  { id: "domains", label: "Domains & HTTPS" },
  { id: "files", label: "Files" },
  { id: "php", label: "PHP" },
  { id: "databases", label: "Databases" },
];

function Gallery() {
  const initial = new URLSearchParams(location.search).get("scheme");
  const [scheme, setScheme] = useState(initial === "dark" ? "dark" : "light");
  document.documentElement.dataset.scheme = scheme;
  const [sort, setSort] = useState<Sort | undefined>({ key: "name", dir: "asc" });
  const [tab, setTab] = useState("overview");

  const columns: Column<Site>[] = [
    { key: "name", header: "Site", cell: (s) => <b>{s.name}</b>, sortValue: (s) => s.name },
    { key: "domain", header: "Domain", cell: (s) => <span className="ui-mono">{s.domain}</span>, sortValue: (s) => s.domain },
    { key: "php", header: "PHP", cell: (s) => <span className="ui-mono">{s.php}</span>, sortValue: (s) => s.php },
    { key: "state", header: "State", cell: (s) => <Badge tone={s.state}>{stateWord[s.state]}</Badge>, sortValue: (s) => s.state },
    { key: "disk", header: "Disk", cell: (s) => <span className="ui-mono">{s.disk} GB</span>, sortValue: (s) => s.disk },
    {
      key: "actions",
      header: "Actions",
      srHeader: true,
      cell: (s) => (
        <RowActions>
          <IconButton label="Edit" icon={<PencilSimple size={16} />} tipBelow={s === sites[0]} />
          <IconButton label="Delete" icon={<Trash size={16} />} danger tipBelow={s === sites[0]} />
        </RowActions>
      ),
    },
  ];

  return (
    <main className="ui-root gallery">
      <header className="gallery-head">
        <h1>Hostashi components</h1>
        <IconButton
          label={scheme === "dark" ? "Switch to light" : "Switch to dark"}
          icon={scheme === "dark" ? <Sun size={20} /> : <Moon size={20} />}
          tipBelow
          onClick={() => setScheme(scheme === "dark" ? "light" : "dark")}
        />
      </header>

      <div className="gallery-row">
        <Button variant="primary" icon={<Plus size={16} />}>
          New site
        </Button>
        <Button icon={<ArrowClockwise size={16} />}>Reload</Button>
        <Button variant="danger" icon={<Trash size={16} />}>
          Delete site
        </Button>
        <Button disabled>Save changes</Button>
      </div>

      <div className="gallery-row">
        <Badge tone="ok">Running</Badge>
        <Badge tone="warn">Expires in 6 days</Badge>
        <Badge tone="err">Failed</Badge>
        <Badge tone="off">Disabled</Badge>
      </div>

      <div className="gallery-stack">
        <Notice tone="err" title="Certificate could not be renewed">
          Let&apos;s Encrypt could not reach blog.example.com over HTTP.
        </Notice>
        <Notice tone="warn" title="PHP 7.4 is end of life">
          The site keeps running. Choose a newer version on the PHP tab.
        </Notice>
        <Notice tone="ok" title="Settings saved" />
        <Notice tone="info" title="Backups run every night at 03:00" />
      </div>

      <Card title="Sites" actions={<Button variant="primary" icon={<Plus size={16} />}>New site</Button>} padded={false}>
        <Table label="Sites" columns={columns} rows={sites} rowKey={(s) => s.name} sort={sort} onSort={setSort} />
      </Card>

      <Card title="shop">
        <Tabs label="shop" tabs={tabs} active={tab} onChange={setTab} idPrefix="g" />
        <div {...tabPanelProps("g", tab)} className="gallery-panel">
          <div className="gallery-form">
            <Field label="Primary domain" hint="Without www; the www name is added as an alias.">
              <input className="ui-input ui-mono" defaultValue="shop.example.com" />
            </Field>
            <Field label="PHP version">
              <select className="ui-select" defaultValue="8.4">
                <option value="8.5">8.5</option>
                <option value="8.4">8.4 (distribution)</option>
                <option value="8.3">8.3</option>
              </select>
            </Field>
            <Field label="Alias" error="blog.example.com is already used by the site blog.">
              <input className="ui-input ui-mono" defaultValue="blog.example.com" />
            </Field>
          </div>
        </div>
      </Card>

      <Card
        title="Certificate"
        actions={
          <RowActions>
            <IconButton label="Renew now" icon={<ArrowClockwise size={16} />} tipBelow />
            <IconButton label="Upload certificate" icon={<Certificate size={16} />} tipBelow />
            <IconButton label="Download" icon={<DownloadSimple size={16} />} tipBelow />
          </RowActions>
        }
      >
        <p className="ui-hint">Let&apos;s Encrypt, valid until 2027-02-18.</p>
      </Card>
    </main>
  );
}

const root = document.getElementById("root");
if (!root) throw new Error("missing #root");
createRoot(root).render(
  <StrictMode>
    <Gallery />
  </StrictMode>,
);
