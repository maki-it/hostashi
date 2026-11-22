// SPDX-License-Identifier: AGPL-3.0-or-later

import {
  ArrowSquareIn,
  Browser,
  ClockCounterClockwise,
  ListMagnifyingGlass,
  Code,
  Database,
  EnvelopeSimple,
  Gauge,
  GearSix,
  Globe,
  Heartbeat,
  Hexagon,
  type Icon,
  Pulse,
  ShieldCheck,
  Signpost,
  SquaresFour,
  UsersThree,
} from "@phosphor-icons/react";
import type { Page } from "./routes";

export type NavItem = { page: Page; label: string; icon: Icon; roles: readonly string[] };
export type NavGroup = { label: string | null; items: readonly NavItem[] };

const all = ["admin", "manager", "user"] as const;
const staff = ["admin", "manager"] as const;
const admin = ["admin"] as const;

// The sidebar (ADR-0037) and the roles that see each page (ADR-0035). The
// server decides what a role may do; this only hides what it cannot use.
export const navGroups: readonly NavGroup[] = [
  { label: null, items: [{ page: "home", label: "Overview", icon: SquaresFour, roles: all }] },
  {
    label: "Hosting",
    items: [
      { page: "sites", label: "Sites", icon: Globe, roles: all },
      { page: "databases", label: "Databases", icon: Database, roles: all },
      { page: "parked", label: "Parked domains", icon: Signpost, roles: staff },
      { page: "pages", label: "Maintenance pages", icon: Browser, roles: staff },
    ],
  },
  {
    label: "Server",
    items: [
      { page: "health", label: "Health", icon: Heartbeat, roles: admin },
      { page: "services", label: "Services", icon: Pulse, roles: admin },
      { page: "system", label: "System", icon: GearSix, roles: admin },
      { page: "php", label: "PHP versions", icon: Code, roles: admin },
      { page: "node", label: "Node.js versions", icon: Hexagon, roles: admin },
      { page: "backups", label: "Backups", icon: ClockCounterClockwise, roles: admin },
      { page: "firewall", label: "Firewall", icon: ShieldCheck, roles: admin },
      { page: "limits", label: "Limit presets", icon: Gauge, roles: staff },
    ],
  },
  {
    label: "Administration",
    items: [
      { page: "users", label: "Users", icon: UsersThree, roles: admin },
      { page: "mail", label: "Email", icon: EnvelopeSimple, roles: admin },
      { page: "import", label: "CloudPanel import", icon: ArrowSquareIn, roles: admin },
      { page: "audit", label: "Audit log", icon: ListMagnifyingGlass, roles: admin },
    ],
  },
];

const items = navGroups.flatMap((g) => g.items.map((item) => ({ ...item, group: g.label })));

/** navItem returns the entry of a page and the label of its group. */
export function navItem(page: Page) {
  return items.find((i) => i.page === page);
}

/** canSee reports whether a role sees a page in the sidebar. */
export function canSee(role: string, page: Page): boolean {
  return navItem(page)?.roles.includes(role) ?? false;
}

/** navFor returns the groups with the items a role sees; empty groups are left out. */
export function navFor(role: string): NavGroup[] {
  return navGroups
    .map((g) => ({ label: g.label, items: g.items.filter((i) => i.roles.includes(role)) }))
    .filter((g) => g.items.length > 0);
}
