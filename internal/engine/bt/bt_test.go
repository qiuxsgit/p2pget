package bt

import (
	"path/filepath"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

func paths(info *metainfo.Info) []string {
	var out []string
	for _, file := range info.UpvertedFiles() {
		out = append(out, filePath(storage.FilePathMakerOpts{Info: info, File: &file}))
	}
	return out
}

func TestFilePath(t *testing.T) {
	file := func(length int64) metainfo.FileTree {
		return metainfo.FileTree{File: metainfo.FileTreeFile{Length: length}}
	}
	cases := []struct {
		name string
		info metainfo.Info
		want []string
	}{
		{"v1 single file", metainfo.Info{Name: "a.iso", Length: 10}, []string{"a.iso"}},
		{"v2 single file", metainfo.Info{Name: "a.iso", MetaVersion: 2, PieceLength: 16384, FileTree: metainfo.FileTree{Dir: map[string]metainfo.FileTree{"a.iso": file(10)}}}, []string{"a.iso"}},
		{"v1 multi file", metainfo.Info{Name: "pack", Files: []metainfo.FileInfo{{Length: 1, Path: []string{"a.txt"}}, {Length: 2, Path: []string{"sub", "b.txt"}}}}, []string{filepath.Join("pack", "a.txt"), filepath.Join("pack", "sub", "b.txt")}},
		{"v1 multi file with one file", metainfo.Info{Name: "pack", Files: []metainfo.FileInfo{{Length: 1, Path: []string{"a.txt"}}}}, []string{filepath.Join("pack", "a.txt")}},
		{"v2 directory with one file", metainfo.Info{Name: "pack", MetaVersion: 2, PieceLength: 16384, FileTree: metainfo.FileTree{Dir: map[string]metainfo.FileTree{"pack": {Dir: map[string]metainfo.FileTree{"a.txt": file(1)}}}}}, []string{filepath.Join("pack", "pack", "a.txt")}},
	}
	for _, c := range cases {
		got := paths(&c.info)
		if len(got) != len(c.want) {
			t.Fatalf("%s: got %v, want %v", c.name, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%s: got %v, want %v", c.name, got, c.want)
			}
		}
	}
}
