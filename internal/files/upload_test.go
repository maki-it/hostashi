// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUploadInChunks(t *testing.T) {
	r, home, _ := newRoot(t)

	data := bytes.Repeat([]byte("0123456789"), (MaxChunk+MaxChunk/2)/10)
	size := int64(len(data))

	u, err := r.StartUpload("up/deep/big.bin", UploadOptions{Size: size})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("without parents: %+v %v", u, err)
	}

	u, err = r.StartUpload("up/deep/big.bin", UploadOptions{Size: size, Parents: true})
	if err != nil {
		t.Fatal(err)
	}

	o := ChunkOptions{ID: u.ID, Size: size}

	// A chunk that does not start where the upload is.
	o.Offset = 5
	if got, err := r.WriteChunk("up/deep/big.bin", bytes.NewReader(data[:10]), o); !errors.Is(err, ErrChanged) || got.Received != 0 {
		t.Fatalf("wrong offset: %+v %v", got, err)
	}

	// Too large a chunk.
	o.Offset = 0
	if _, err := r.WriteChunk("up/deep/big.bin", bytes.NewReader(data), o); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("too large: %v", err)
	}

	got, err := r.WriteChunk("up/deep/big.bin", bytes.NewReader(data[:MaxChunk]), o)
	if err != nil || got.Done || got.Received != MaxChunk {
		t.Fatalf("first chunk: %+v %v", got, err)
	}

	if _, err := os.Stat(filepath.Join(home, "up", "deep", "big.bin")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("target visible before the last chunk")
	}

	o.Offset = MaxChunk

	got, err = r.WriteChunk("up/deep/big.bin", bytes.NewReader(data[MaxChunk:]), o)
	if err != nil || !got.Done || got.Entry == nil || got.Entry.Size != size {
		t.Fatalf("last chunk: %+v %v", got, err)
	}

	b, _ := os.ReadFile(filepath.Join(home, "up", "deep", "big.bin"))
	if !bytes.Equal(b, data) {
		t.Fatal("content differs")
	}

	if st, _ := os.Stat(filepath.Join(home, "up", "deep", "big.bin")); st.Mode().Perm() != 0o644 {
		t.Errorf("mode %v", st.Mode())
	}

	ents, _ := os.ReadDir(filepath.Join(home, "up", "deep"))
	if len(ents) != 1 {
		t.Errorf("left over: %v", ents)
	}

	// The part is gone after the last chunk.
	if _, err := r.WriteChunk("up/deep/big.bin", bytes.NewReader(nil), o); !errors.Is(err, ErrNotFound) {
		t.Fatalf("chunk after done: %v", err)
	}
}

func TestUploadTargets(t *testing.T) {
	r, home, outside := newRoot(t)

	if _, err := r.StartUpload("htdocs/index.php", UploadOptions{Size: 1}); !errors.Is(err, ErrExists) {
		t.Fatalf("existing: %v", err)
	}

	if _, err := r.StartUpload("htdocs", UploadOptions{Size: 1, Overwrite: true}); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("over a folder: %v", err)
	}

	if _, err := r.StartUpload("link/x", UploadOptions{Size: 1, Parents: true}); !errors.Is(err, ErrSymlink) {
		t.Fatalf("through a symlink: %v", err)
	}

	if _, err := os.Stat(filepath.Join(outside, "x")); err == nil {
		t.Fatal("wrote outside the home")
	}

	if _, err := r.StartUpload("", UploadOptions{}); !errors.Is(err, ErrHome) {
		t.Fatalf("home: %v", err)
	}

	// Overwrite keeps the old mode; an empty file is done at once.
	if err := os.Chmod(filepath.Join(home, "htdocs", "index.php"), 0o640); err != nil {
		t.Fatal(err)
	}

	u, err := r.StartUpload("htdocs/index.php", UploadOptions{Size: 0, Overwrite: true})
	if err != nil || !u.Done {
		t.Fatalf("empty overwrite: %+v %v", u, err)
	}

	if st, _ := os.Stat(filepath.Join(home, "htdocs", "index.php")); st.Size() != 0 || st.Mode().Perm() != 0o640 {
		t.Errorf("after overwrite: %v %d", st.Mode(), st.Size())
	}

	// A file that appeared during the upload is not replaced.
	u, err = r.StartUpload("htdocs/race.txt", UploadOptions{Size: 2})
	if err != nil {
		t.Fatal(err)
	}

	write(t, filepath.Join(home, "htdocs", "race.txt"), "theirs")

	if _, err := r.WriteChunk("htdocs/race.txt", strings.NewReader("ok"), ChunkOptions{ID: u.ID, Size: 2}); !errors.Is(err, ErrExists) {
		t.Fatalf("race: %v", err)
	}

	if read(t, filepath.Join(home, "htdocs", "race.txt")) != "theirs" {
		t.Fatal("replaced a file that appeared meanwhile")
	}

	for _, id := range []string{"", "../../x", "ABCDEF0123456789", "0123"} {
		if _, err := r.WriteChunk("htdocs/race.txt", strings.NewReader(""), ChunkOptions{ID: id}); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("id %q: %v", id, err)
		}
	}
}

func TestCancelAndOldParts(t *testing.T) {
	r, home, _ := newRoot(t)

	u, err := r.StartUpload("htdocs/x.bin", UploadOptions{Size: 10})
	if err != nil {
		t.Fatal(err)
	}

	part := filepath.Join(home, "htdocs", partName(u.ID))
	if _, err := os.Stat(part); err != nil {
		t.Fatal(err)
	}

	if err := r.CancelUpload("htdocs/x.bin", u.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(part); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("part left after cancel")
	}

	// An abandoned part older than a day goes with the next upload.
	old := filepath.Join(home, "htdocs", partName("00000000000000aa"))
	write(t, old, "x")

	past := time.Now().Add(-2 * partMaxAge)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}

	fresh := filepath.Join(home, "htdocs", partName("00000000000000bb"))
	write(t, fresh, "x")

	if _, err := r.StartUpload("htdocs/y.bin", UploadOptions{Size: 1}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
		t.Error("old part kept")
	}

	if _, err := os.Stat(fresh); err != nil {
		t.Error("fresh part removed")
	}
}
