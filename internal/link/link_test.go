package link

import (
	"testing"

	"github.com/anacrolix/torrent/metainfo"
)

const exampleMagnet = "magnet:?xt=urn:btih:22ad209951fab375a766d6aba5c517dabf77295a&dn=example.iso"
const exampleED2K = "ed2k://|file|example.iso|9046644736|5BDA970B9C939F3B65DDF8E99AFE92D6|/"

func TestParseValidLinks(t *testing.T) {
	for _, tc := range []struct {
		value, kind, id string
		size            int64
	}{
		{exampleMagnet, "bt", "22ad209951fab375a766d6aba5c517dabf77295a", 0},
		{exampleED2K, "ed2k", "5bda970b9c939f3b65ddf8e99afe92d6", 9046644736},
	} {
		got, err := Parse(tc.value)
		if err != nil {
			t.Fatal(err)
		}
		if got.Kind != tc.kind || got.ID != tc.id || got.Size != tc.size {
			t.Fatalf("unexpected Request: %+v", got)
		}
	}
}

func TestRejectInvalidAndUnsafeLinks(t *testing.T) {
	for _, value := range []string{
		"", "https://example.com/file.iso", "magnet:?dn=missing-hash", "magnet:?xt=urn:btih:abc",
		"ed2k://|file|../escape.iso|1|5BDA970B9C939F3B65DDF8E99AFE92D6|/",
		"ed2k://|file|%2Ftmp%2Fescape|1|5BDA970B9C939F3B65DDF8E99AFE92D6|/",
		"ed2k://|file|file.iso|-1|5BDA970B9C939F3B65DDF8E99AFE92D6|/",
		"ed2k://|file|file.iso|1|not-a-hash|/",
	} {
		if _, err := Parse(value); err == nil {
			t.Errorf("accepted invalid input %q", value)
		}
	}
}

func TestPortableFileNames(t *testing.T) {
	for _, name := range []string{"../x", `C:\x`, "a/b", `a\b`, "a\x00b", ".", "CON", "nul.txt", "x:stream", "trailing.", "trailing "} {
		if err := ValidateFileName(name); err == nil {
			t.Errorf("accepted unsafe filename %q", name)
		}
	}
	for _, name := range []string{"Windows 11.iso", "中文镜像.iso", "book.epub"} {
		if err := ValidateFileName(name); err != nil {
			t.Errorf("rejected safe filename: %v", err)
		}
	}
}

func TestRejectTorrentCaseAndStatePathCollisions(t *testing.T) {
	for _, info := range []metainfo.Info{
		{Name: ".p2pget", Length: 1},
		{Name: "bundle", Files: []metainfo.FileInfo{{Path: []string{"Foo.bin"}, Length: 1}, {Path: []string{"foo.bin"}, Length: 1}}},
		{Name: "bundle", Files: []metainfo.FileInfo{{Path: []string{"a"}, Length: 1}, {Path: []string{"a", "b"}, Length: 1}}},
	} {
		if err := ValidateTorrentInfo(&info); err == nil {
			t.Errorf("accepted conflicting torrent paths: %+v", info)
		}
	}
}
