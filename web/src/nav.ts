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
  HardDrives,
  Globe,
  Heartbeat,
  type Icon,
  Pulse,
  ShieldCheck,
  Signpost,
  SquaresFour,
  Stack,
  UsersThree,
} from "@phosphor-icons/react";
import i18n from "./i18n";
import type { Catalog } from "./locales/en";
import type { Page } from "./routes";

type NavPage = Page & keyof Catalog["shell"]["nav"]["pages"];
type GroupID = keyof Catalog["shell"]["nav"]["groups"];

/**
 * An entry of the sidebar; its label is shell.nav.pages.<page> in the
 * catalog (ADR-0071). also: more pages the entry stands for, such as the
 * tabs of Runtimes.
 */
export type NavItem = { page: NavPage; icon: Icon; roles: readonly string[]; also?: readonly Page[] };
/** A group of the sidebar; id null is the top without a heading. */
export type NavGroup = { id: GroupID | null; items: readonly NavItem[] };

/** pageLabel is the sidebar label of an entry in the current language. */
export const pageLabel = (i: { page: NavPage }): string => i18n.t(`shell.nav.pages.${i.page}`);

/** groupLabel is the heading of a group in the current language, "" for the top. */
export const groupLabel = (id: GroupID | null): string => (id ? i18n.t(`shell.nav.groups.${id}`) : "");

const all = ["admin", "manager", "user"] as const;
const staff = ["admin", "manager"] as const;
const admin = ["admin"] as const;

// The sidebar (ADR-0037) and the roles that see each page (ADR-0035). The
// server decides what a role may do; this only hides what it cannot use.
export const navGroups: readonly NavGroup[] = [
  { id: null, items: [{ page: "home", icon: SquaresFour, roles: all }] },
  {
    id: "hosting",
    items: [
      { page: "sites", icon: Globe, roles: all },
      { page: "databases", icon: Database, roles: all },
      { page: "parked", icon: Signpost, roles: staff },
      { page: "pages", icon: Browser, roles: staff },
      { page: "limits", icon: Gauge, roles: staff },
      { page: "profiles", icon: Stack, roles: admin },
    ],
  },
  {
    id: "server",
    items: [
      { page: "health", icon: Heartbeat, roles: admin },
      { page: "services", icon: Pulse, roles: admin },
      { page: "system", icon: GearSix, roles: admin },
      { page: "pools", icon: HardDrives, roles: admin },
      { page: "php", icon: Code, roles: admin, also: ["node", "python"] },
      { page: "backups", icon: ClockCounterClockwise, roles: admin },
      { page: "firewall", icon: ShieldCheck, roles: admin },
    ],
  },
  {
    id: "administration",
    items: [
      { page: "users", icon: UsersThree, roles: admin },
      { page: "mail", icon: EnvelopeSimple, roles: admin },
      { page: "import", icon: ArrowSquareIn, roles: admin },
      { page: "audit", icon: ListMagnifyingGlass, roles: admin },
    ],
  },
];

const items = navGroups.flatMap((g) => g.items.map((item) => ({ ...item, group: g.id })));

/** navItem returns the entry of a page and the id of its group. */
export function navItem(page: Page) {
  return items.find((i) => i.page === page || i.also?.includes(page));
}

/** canSee reports whether a role sees a page in the sidebar. */
export function canSee(role: string, page: Page): boolean {
  return navItem(page)?.roles.includes(role) ?? false;
}

/** navFor returns the groups with the items a role sees; empty groups are left out. */
export function navFor(role: string): NavGroup[] {
  return navGroups
    .map((g) => ({ id: g.id, items: g.items.filter((i) => i.roles.includes(role)) }))
    .filter((g) => g.items.length > 0);
}
