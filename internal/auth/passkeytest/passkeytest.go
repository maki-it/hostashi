// SPDX-License-Identifier: AGPL-3.0-or-later

// Package passkeytest is a software authenticator for tests. It answers the
// options of navigator.credentials.create and .get the way a browser with a
// platform authenticator would, with "none" attestation and an ES256 key.
// It is only imported by tests.
package passkeytest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fxamacker/cbor/v2"
)

// Authenticator flags (WebAuthn §6.1).
const (
	FlagUP = 0x01 // user present
	FlagUV = 0x04 // user verified
	FlagAT = 0x40 // attested credential data included
)

// Authenticator holds one passkey.
type Authenticator struct {
	Origin string // for example https://panel.example.com
	// Flags used for assertions; default UP|UV.
	Flags byte
	// SignCount is the counter of the next assertion. Tests can lower it to
	// simulate a cloned authenticator.
	SignCount uint32

	key        *ecdsa.PrivateKey
	credID     []byte
	userHandle []byte
	rpID       string
}

var b64 = base64.RawURLEncoding

// New creates an authenticator for origin with a fresh key.
func New(origin string) (*Authenticator, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	id := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}

	return &Authenticator{Origin: origin, Flags: FlagUP | FlagUV, SignCount: 1, key: key, credID: id}, nil
}

// CredentialID returns the id of the passkey.
func (a *Authenticator) CredentialID() []byte { return a.credID }

type creationOptions struct {
	PublicKey struct {
		Challenge string `json:"challenge"`
		RP        struct {
			ID string `json:"id"`
		} `json:"rp"`
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	} `json:"publicKey"`
}

type requestOptions struct {
	PublicKey struct {
		Challenge string `json:"challenge"`
		RPID      string `json:"rpId"`
	} `json:"publicKey"`
}

func (a *Authenticator) clientData(typ, challenge string) []byte {
	b, _ := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": a.Origin, "crossOrigin": false})

	return b
}

func authData(rpID string, flags byte, count uint32, attested []byte) []byte {
	h := sha256.Sum256([]byte(rpID))
	out := append([]byte{}, h[:]...)
	out = append(out, flags)
	out = binary.BigEndian.AppendUint32(out, count)

	return append(out, attested...)
}

func (a *Authenticator) coseKey() ([]byte, error) {
	pub, err := a.key.PublicKey.ECDH()
	if err != nil {
		return nil, err
	}

	raw := pub.Bytes() // 0x04 || X || Y
	if len(raw) != 65 {
		return nil, errors.New("unexpected key size")
	}

	return cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: raw[1:33], -3: raw[33:]})
}

// Register answers creation options (the JSON the server sent) and returns
// the credential JSON a browser would post back. flags defaults to UP|UV|AT.
func (a *Authenticator) Register(options []byte, flags byte) ([]byte, error) {
	var o creationOptions
	if err := json.Unmarshal(options, &o); err != nil {
		return nil, fmt.Errorf("options: %w", err)
	}

	a.rpID = o.PublicKey.RP.ID

	if a.userHandle, _ = b64.DecodeString(o.PublicKey.User.ID); a.userHandle == nil {
		return nil, errors.New("options: no user id")
	}

	if flags == 0 {
		flags = FlagUP | FlagUV | FlagAT
	}

	key, err := a.coseKey()
	if err != nil {
		return nil, err
	}

	attested := make([]byte, 16, 16+2+len(a.credID)+len(key))                 // the AAGUID is 16 zero bytes for "none"
	attested = binary.BigEndian.AppendUint16(attested, uint16(len(a.credID))) //nolint:gosec // the id is 32 bytes
	attested = append(attested, a.credID...)
	attested = append(attested, key...)

	obj, err := cbor.Marshal(map[string]any{
		"fmt": "none", "attStmt": map[string]any{}, "authData": authData(a.rpID, flags, 0, attested),
	})
	if err != nil {
		return nil, err
	}

	return json.Marshal(map[string]any{
		"id": b64.EncodeToString(a.credID), "rawId": b64.EncodeToString(a.credID), "type": "public-key",
		"response": map[string]string{
			"clientDataJSON":    b64.EncodeToString(a.clientData("webauthn.create", o.PublicKey.Challenge)),
			"attestationObject": b64.EncodeToString(obj),
		},
		"clientExtensionResults": map[string]any{},
	})
}

// Login answers request options and returns the assertion JSON.
func (a *Authenticator) Login(options []byte) ([]byte, error) {
	var o requestOptions
	if err := json.Unmarshal(options, &o); err != nil {
		return nil, fmt.Errorf("options: %w", err)
	}

	rpID := o.PublicKey.RPID
	if rpID == "" {
		rpID = a.rpID
	}

	ad := authData(rpID, a.Flags, a.SignCount, nil)
	a.SignCount++

	cd := a.clientData("webauthn.get", o.PublicKey.Challenge)
	cdHash := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, ad...), cdHash[:]...))

	sig, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		return nil, err
	}

	return json.Marshal(map[string]any{
		"id": b64.EncodeToString(a.credID), "rawId": b64.EncodeToString(a.credID), "type": "public-key",
		"response": map[string]string{
			"clientDataJSON":    b64.EncodeToString(cd),
			"authenticatorData": b64.EncodeToString(ad),
			"signature":         b64.EncodeToString(sig),
			"userHandle":        b64.EncodeToString(a.userHandle),
		},
		"clientExtensionResults": map[string]any{},
	})
}
