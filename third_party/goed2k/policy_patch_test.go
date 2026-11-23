package goed2k

import (
	"github.com/goed2k/core/protocol"
	"os"
	"testing"
)

func TestPeerRecordsRemainStableWhenSourcesChange(t *testing.T) {
	session := NewSession(NewSettings())
	file, err := os.CreateTemp(t.TempDir(), "payload")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	transfer, err := NewTransfer(session, NewAddTransferParamsFromFile(protocol.EMule, CurrentTimeMillis(), 1024, file, false))
	if err != nil {
		t.Fatal(err)
	}
	original := protocol.NewEndpoint(0x0200007f, 5000)
	if _, err = transfer.policy.AddPeer(NewPeerWithSource(original, true, int(PeerServer))); err != nil {
		t.Fatal(err)
	}
	record := transfer.policy.Get(original)
	connection := NewPeerConnection(session, original, transfer, record)
	transfer.policy.SetConnection(record, connection)
	for i := 0; i < 25; i++ {
		_, err = transfer.policy.AddPeer(NewPeerWithSource(protocol.NewEndpoint(int32(0x0100007f+i), 6000+i), true, int(PeerServer)))
		if err != nil {
			t.Fatal(err)
		}
	}
	if transfer.policy.Get(original) != record {
		t.Fatal("inserting sources moved an existing peer record")
	}
	transfer.policy.ConnectionClosed(connection, CurrentTime())
	if transfer.policy.Get(original).Connection != nil {
		t.Fatal("closed connection remained attached to canonical peer")
	}
}
