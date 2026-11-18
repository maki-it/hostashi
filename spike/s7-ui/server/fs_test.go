package main

import (
	"io/fs"
	"testing/fstest"
)

type mapFS map[string]string

func (m mapFS) fs() fs.FS {
	t := fstest.MapFS{}
	for k, v := range m {
		t[k] = &fstest.MapFile{Data: []byte(v)}
	}
	return t
}
