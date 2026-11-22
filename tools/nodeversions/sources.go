// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
)

// Upstream sources.
const (
	indexURL    = "https://nodejs.org/dist/index.json"
	scheduleURL = "https://raw.githubusercontent.com/nodejs/Release/main/schedule.json"
	// shasumsURL takes the version with its "v".
	shasumsURL = "https://nodejs.org/dist/%s/SHASUMS256.txt.asc"
	maxBody    = 8 << 20
)

// releaseKeys are the public keys of the Node.js release team, from
// https://github.com/nodejs/release-keys (keys/<fingerprint>.asc of every
// key in keys.list). A release signed by another key fails the run: a new
// release key is added here by hand after checking it there.
//
//go:embed release-keys.asc
var releaseKeys []byte

// IndexEntry is one release in nodejs.org/dist/index.json. LTS is the
// codename of an LTS release, false otherwise.
type IndexEntry struct {
	Version  string          `json:"version"`
	Date     string          `json:"date"`
	LTS      json.RawMessage `json:"lts"`
	Security bool            `json:"security"`
}

// Codename returns the LTS codename, "" for a release that is not LTS.
func (e IndexEntry) Codename() string {
	var s string
	if json.Unmarshal(e.LTS, &s) != nil {
		return ""
	}

	return s
}

// ParseIndex reads nodejs.org/dist/index.json.
func ParseIndex(b []byte) ([]IndexEntry, error) {
	var out []IndexEntry
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("index.json: %w", err)
	}

	if len(out) == 0 {
		return nil, errors.New("index.json: no releases")
	}

	return out, nil
}

// Line is one release line in the Node.js release schedule.
type Line struct {
	Start    string `json:"start"`
	LTS      string `json:"lts"`
	End      string `json:"end"`
	Codename string `json:"codename"`
}

// ParseSchedule reads schedule.json, keyed by "v24" and so on.
func ParseSchedule(b []byte) (map[string]Line, error) {
	var out map[string]Line
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("schedule.json: %w", err)
	}

	return out, nil
}

// VerifySums checks the clear-signed SHASUMS256.txt of a release against
// keyring and returns the SHA-256 of the linux-x64 and linux-arm64
// .tar.gz builds by arch.
func VerifySums(signed, keyring []byte, version string) (map[string]string, error) {
	keys, err := readKeys(keyring)
	if err != nil {
		return nil, err
	}

	block, _ := clearsign.Decode(signed)
	if block == nil {
		return nil, fmt.Errorf("SHASUMS256.txt of %s is not clear-signed", version)
	}

	if _, err := block.VerifySignature(keys, nil); err != nil {
		return nil, fmt.Errorf("SHASUMS256.txt of %s: signature: %w", version, err)
	}

	want := map[string]string{
		"x64":   "node-" + version + "-linux-x64.tar.gz",
		"arm64": "node-" + version + "-linux-arm64.tar.gz",
	}
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(block.Plaintext))

	for sc.Scan() {
		sum, name, ok := strings.Cut(strings.TrimSpace(sc.Text()), "  ")
		if !ok {
			continue
		}

		for arch, file := range want {
			if name == file {
				if b, err := hex.DecodeString(sum); err != nil || len(b) != 32 {
					return nil, fmt.Errorf("SHASUMS256.txt of %s: bad sum for %s", version, file)
				}

				out[arch] = sum
			}
		}
	}

	for arch, file := range want {
		if out[arch] == "" {
			return nil, fmt.Errorf("SHASUMS256.txt of %s has no %s", version, file)
		}
	}

	return out, nil
}

const keyBegin = "-----BEGIN PGP PUBLIC KEY BLOCK-----"

// readKeys reads every armored key block of keyring; ReadArmoredKeyRing
// reads only the first.
func readKeys(keyring []byte) (openpgp.EntityList, error) {
	var out openpgp.EntityList

	for block := range strings.SplitSeq(string(keyring), keyBegin) {
		if strings.TrimSpace(block) == "" {
			continue
		}

		el, err := openpgp.ReadArmoredKeyRing(strings.NewReader(keyBegin + block))
		if err != nil {
			return nil, fmt.Errorf("release keys: %w", err)
		}

		out = append(out, el...)
	}

	if len(out) == 0 {
		return nil, errors.New("release keys: none")
	}

	return out, nil
}

// Fetcher downloads the upstream data.
type Fetcher struct {
	Client *http.Client
}

func (f Fetcher) bytes(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}

	req.Header.Set("User-Agent", "hostashi-nodeversions")

	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}

	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}

	return b, nil
}

// Index fetches the list of releases.
func (f Fetcher) Index(ctx context.Context) ([]IndexEntry, error) {
	b, err := f.bytes(ctx, indexURL)
	if err != nil {
		return nil, err
	}

	return ParseIndex(b)
}

// Schedule fetches the release schedule.
func (f Fetcher) Schedule(ctx context.Context) (map[string]Line, error) {
	b, err := f.bytes(ctx, scheduleURL)
	if err != nil {
		return nil, err
	}

	return ParseSchedule(b)
}

// Sums fetches and verifies the checksums of a release ("24.22.3").
func (f Fetcher) Sums(ctx context.Context, version string) (map[string]string, error) {
	b, err := f.bytes(ctx, fmt.Sprintf(shasumsURL, "v"+version))
	if err != nil {
		return nil, err
	}

	return VerifySums(b, releaseKeys, "v"+version)
}
