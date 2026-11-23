package ed2k

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	goed2k "github.com/goed2k/core"
	"github.com/goed2k/core/protocol"
	"github.com/qiuxsgit/p2pget/internal/link"
)

func TestED2KResumeDoesNotTrustChangedOrMissingFiles(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "payload.bin")
	payload := []byte("verified bytes")
	hash, _ := protocol.HashFromData(payload)
	req, err := link.Parse(goed2k.FormatLink("payload.bin", int64(len(payload)), hash))
	if err != nil {
		t.Fatal(err)
	}
	bits := protocol.NewBitField(1)
	bits.SetBit(0)
	state := goed2k.ClientState{Version: 7, Transfers: []goed2k.ClientTransferState{{Hash: hash, Size: int64(len(payload)), TargetPath: target, ResumeData: &protocol.TransferResumeData{Hashes: []protocol.Hash{hash}, Pieces: bits}}}}
	raw, _ := json.Marshal(state)
	path := filepath.Join(root, "state.json")
	os.WriteFile(path, raw, 0600)
	store := taskEDStore{path: path, target: target, identity: filepath.Join(root, "id.pem"), req: req}
	for _, tc := range []struct {
		name  string
		data  []byte
		valid bool
	}{{"missing", nil, false}, {"corrupt", []byte("changed  bytes"), false}, {"correct", payload, true}} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.data != nil {
				os.WriteFile(target, tc.data, 0600)
			} else {
				os.Remove(target)
			}
			loaded, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			if got := loaded.Transfers[0].ResumeData.Pieces.GetBit(0); got != tc.valid {
				t.Fatalf("completion=%v want=%v", got, tc.valid)
			}
		})
	}
}
