package torrent

import (
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/anacrolix/missinggo/pubsub"
	"github.com/bradfitz/iter"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"

	pp "github.com/james-lawrence/torrent/btprotocol"
	"github.com/james-lawrence/torrent/metainfo"
	"github.com/james-lawrence/torrent/storage"
)

// Ensure that no race exists between sending a bitfield, and a subsequent
// Have that would potentially alter it.
func TestSendBitfieldThenHave(t *testing.T) {
	t.SkipNow()
	cl := Client{
		config: TestingConfig(t),
	}
	ts, err := New(metainfo.Hash{})
	require.NoError(t, err)
	tt, err := cl.newTorrent(ts)
	require.NoError(t, err)
	tt.setInfo(&metainfo.Info{
		Pieces:      make([]byte, metainfo.HashSize*3),
		Length:      24 * (1 << 10),
		PieceLength: 8 * (1 << 10),
	})
	c := cl.newConnection(nil, false, IpPort{})
	c.setTorrent(tt)

	r, w := io.Pipe()
	c.r = r
	c.w = w
	go c.writer(time.Minute)
	c.cmu().Lock()
	c.t.chunks.completed.Add(1)
	c.PostBitfield( /*[]bool{false, true, false}*/ )
	c.cmu().Unlock()
	c.cmu().Lock()
	c.Have(2)
	c.cmu().Unlock()
	b := make([]byte, 15)
	n, err := io.ReadFull(r, b)
	c.cmu().Lock()
	// This will cause connection.writer to terminate.
	c.closed.Set()
	c.cmu().Unlock()
	require.NoError(t, err)
	require.EqualValues(t, 15, n)
	// Here we see that the bitfield doesn't have piece 2 set, as that should
	// arrive in the following Have message.
	require.EqualValues(t, "\x00\x00\x00\x02\x05@\x00\x00\x00\x05\x04\x00\x00\x00\x02", string(b))
}

type torrentStorage struct {
	writeSem sync.Mutex
}

func (me *torrentStorage) Close() error { return nil }

func (me *torrentStorage) Completion() storage.Completion {
	return storage.Completion{}
}

func (me *torrentStorage) MarkComplete() error {
	return nil
}

func (me *torrentStorage) MarkNotComplete() error {
	return nil
}

func (me *torrentStorage) ReadAt([]byte, int64) (int, error) {
	panic("shouldn't be called")
}

func (me *torrentStorage) WriteAt(b []byte, _ int64) (int, error) {
	if len(b) != defaultChunkSize {
		panic(len(b))
	}
	me.writeSem.Unlock()
	return len(b), nil
}

func BenchmarkConnectionMainReadLoop(b *testing.B) {
	cl := &Client{
		config: &ClientConfig{
			DownloadRateLimiter: rate.NewLimiter(rate.Inf, 0),
		},
	}

	ts := &torrentStorage{}
	t := &torrent{
		cln:               cl,
		storage:           storage.NewTorrent(ts),
		pieceStateChanges: pubsub.NewPubSub(),
	}
	require.NoError(b, t.setInfo(&metainfo.Info{
		Pieces:      make([]byte, 20),
		Length:      1 << 20,
		PieceLength: 1 << 20,
	}))
	t.setChunkSize(defaultChunkSize)
	// t.makePieces()
	t.chunks.ChunksPend(0)
	r, w := net.Pipe()
	cn := cl.newConnection(r, true, IpPort{})
	cn.setTorrent(t)
	mrlErr := make(chan error)
	msg := pp.Message{
		Type:  pp.Piece,
		Piece: make([]byte, defaultChunkSize),
	}
	go func() {
		cl.lock()
		err := cn.mainReadLoop()
		if err != nil {
			mrlErr <- err
		}
		close(mrlErr)
	}()
	wb := msg.MustMarshalBinary()
	b.SetBytes(int64(len(msg.Piece)))
	go func() {
		defer w.Close()
		ts.writeSem.Lock()
		for range iter.N(b.N) {
			cl.lock()
			// The chunk must be written to storage everytime, to ensure the
			// writeSem is unlocked.
			// TODO
			// t.pieces[0].dirtyChunks.Clear()
			cl.unlock()
			n, err := w.Write(wb)
			require.NoError(b, err)
			require.EqualValues(b, len(wb), n)
			ts.writeSem.Lock()
		}
	}()
	require.NoError(b, <-mrlErr)
	require.EqualValues(b, b.N, cn.stats.ChunksReadUseful.Int64())
}

func TestPexPeerFlags(t *testing.T) {
	var testcases = []struct {
		conn *connection
		f    pp.PexPeerFlags
	}{
		{&connection{outgoing: false, PeerPrefersEncryption: false}, 0},
		{&connection{outgoing: false, PeerPrefersEncryption: true}, pp.PexPrefersEncryption},
		{&connection{outgoing: true, PeerPrefersEncryption: false}, pp.PexOutgoingConn},
		{&connection{outgoing: true, PeerPrefersEncryption: true}, pp.PexOutgoingConn | pp.PexPrefersEncryption},
	}
	for i, tc := range testcases {
		f := tc.conn.pexPeerFlags()
		require.EqualValues(t, tc.f, f, i)
	}
}
