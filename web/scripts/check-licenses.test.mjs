// SPDX-License-Identifier: AGPL-3.0-or-later

import { allowedFor, bundledPackages, bundledPaths, check, licenseAllowed } from "./check-licenses.mjs";

describe("licenseAllowed", () => {
  it.each(["MIT", "Apache-2.0", " BSD-3-Clause ", "(MIT OR Apache-2.0)", "MIT OR GPL-2.0-only", "MIT AND ISC"])(
    "accepts %s",
    (expr) => expect(licenseAllowed(expr)).toBe(true),
  );
  it.each([
    "GPL-2.0-only",
    "SSPL-1.0",
    "MIT AND GPL-2.0-only",
    "GPL-2.0-only WITH Classpath-exception-2.0",
    "(MIT OR ISC) AND BSD-3-Clause",
    "MIT OR ISC AND BSD-3-Clause",
    "LicenseRef-Proprietary",
    "SEE LICENSE IN LICENSE.txt",
    "mit",
    "",
    undefined,
    null,
    { type: "MIT" },
  ])("rejects %j", (expr) => expect(licenseAllowed(expr)).toBe(false));
});

describe("allowedFor", () => {
  it("allows OFL-1.1 only for font packages", () => {
    expect(allowedFor("@fontsource/sora", "OFL-1.1")).toBe(true);
    expect(allowedFor("@fontsource/ibm-plex-mono", "MIT")).toBe(true);
    expect(allowedFor("some-code", "OFL-1.1")).toBe(false);
    expect(allowedFor("@fontsource-evil/x", "OFL-1.1")).toBe(false);
    expect(allowedFor("@fontsource/sora", "GPL-3.0-only")).toBe(false);
    expect(allowedFor("@fontsource/sora", undefined)).toBe(false);
  });
});

describe("check", () => {
  const lock = {
    lockfileVersion: 3,
    packages: {
      "": {
        name: "hostashi-web",
        license: "AGPL-3.0-or-later",
        dependencies: { react: "1", a: "1", nolicense: "1", "@fontsource/sora": "1", "ofl-code": "1" },
        devDependencies: { vitest: "1" },
        optionalDependencies: { fsevents: "1" },
      },
      "node_modules/a": { dependencies: { b: "1" }, license: "MIT" },
      "node_modules/react": { license: "MIT" },
      "node_modules/a/node_modules/b": { license: "GPL-3.0-only" },
      "node_modules/nolicense": {},
      "node_modules/@fontsource/sora": { license: "OFL-1.1" },
      "node_modules/ofl-code": { license: "OFL-1.1" },
      "node_modules/vitest": { dev: true, license: "GPL-3.0-only" },
      "node_modules/fsevents": { devOptional: true, license: "Unknown" },
    },
  };

  it("skips the root and dev packages and strips node_modules paths", () => {
    expect(bundledPackages(lock).map(([n]) => n)).toEqual(["a", "react", "b", "nolicense", "@fontsource/sora", "ofl-code"]);
  });

  it("reports packages with disallowed or missing licenses", () => {
    expect(check(lock)).toEqual(["b: GPL-3.0-only", "nolicense: (no license field)", "ofl-code: OFL-1.1"]);
  });

  // npm drops "dev" from the optional platform packages of typescript 7,
  // which is devOptional; they are not bundled all the same.
  it("follows dependencies, not the dev flags", () => {
    const lock = {
      lockfileVersion: 3,
      packages: {
        "": { dependencies: { app: "1" }, devDependencies: { typescript: "7" } },
        "node_modules/app": {
          dependencies: { shared: "1" },
          optionalDependencies: { "app-linux": "1", "app-win32": "1" },
          peerDependencies: { react: "1", "@types/react": "1" },
          peerDependenciesMeta: { "@types/react": { optional: true } },
        },
        "node_modules/app/node_modules/shared": { license: "MIT" },
        "node_modules/shared": { license: "GPL-3.0-only" },
        "node_modules/app-linux": { optional: true, license: "MIT" },
        "node_modules/react": { license: "MIT" },
        "node_modules/@types/react": { dev: true, license: "MIT" },
        "node_modules/typescript": { devOptional: true, optionalDependencies: { "@typescript/typescript-aix-ppc64": "7" } },
        "node_modules/@typescript/typescript-aix-ppc64": { optional: true, license: "Apache-2.0" },
      },
    };
    expect(bundledPaths(lock)).toEqual([
      "node_modules/app",
      "node_modules/app/node_modules/shared",
      "node_modules/app-linux",
      "node_modules/react",
    ]);
  });

  it("rejects lockfiles without packages", () => {
    expect(() => check({ lockfileVersion: 1, dependencies: {} })).toThrow(/lockfileVersion 2/);
    expect(() => check(null)).toThrow();
  });
});
