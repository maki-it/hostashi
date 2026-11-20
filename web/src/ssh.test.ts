// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { ApiError } from "./api";
import { keyProblem, labelProblem, sshMessage, suggestLabel } from "./ssh";

const ed = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl";

describe("keyProblem", () => {
  it("accepts a key with and without a comment", () => {
    expect(keyProblem(ed)).toBe("");
    expect(keyProblem("  " + ed + " kim@laptop\n")).toBe("");
  });

  it("refuses what is not one public key", () => {
    expect(keyProblem("")).toMatch(/Paste a public key/);
    expect(keyProblem("-----BEGIN OPENSSH PRIVATE KEY-----\nabc")).toMatch(/private key/);
    expect(keyProblem(ed + "\n" + ed)).toMatch(/one line/);
    expect(keyProblem("ssh-dss AAAA")).toMatch(/not look like a supported/);
    expect(keyProblem('command="sh" ' + ed)).toMatch(/not look like a supported/);
  });
});

describe("labelProblem", () => {
  it("follows the server's rules", () => {
    expect(labelProblem("kim@laptop")).toBe("");
    expect(labelProblem("")).toMatch(/Enter a label/);
    expect(labelProblem(" x")).toMatch(/start or end/);
    expect(labelProblem("a;b")).toMatch(/letters, digits/);
    expect(labelProblem("x".repeat(65))).toMatch(/longer than 64/);
  });
});

describe("suggestLabel", () => {
  it("takes the comment and drops what a label cannot hold", () => {
    expect(suggestLabel(ed + " kim@laptop")).toBe("kim@laptop");
    expect(suggestLabel(ed + " Kim's (work) PC")).toBe("Kims work PC");
    expect(suggestLabel(ed)).toBe("");
  });
});

describe("sshMessage", () => {
  it("turns error codes into sentences", () => {
    expect(sshMessage(new ApiError(409, "ssh_key_exists", "x"))).toBe("The site already has this key.");
    expect(sshMessage(new ApiError(422, "invalid_argument", "hosting: invalid argument: sshkeys: invalid: RSA keys need at least 3072 bits"))).toBe(
      "The key was refused: RSA keys need at least 3072 bits",
    );
    expect(sshMessage(new ApiError(404, "not_found", "no such SSH key"))).toMatch(/does not exist any more/);
  });
});
