package torrent

import (
	"sync"

	"github.com/anacrolix/multiless"
	"github.com/google/btree"
)

// Peers are stored with their priority at insertion. Their priority may
// change if our apparent IP changes, we don't currently handle that.
type prioritizedPeer struct {
	prio peerPriority
	p    Peer
}

func (me prioritizedPeer) Less(than btree.Item) bool {
	other := than.(prioritizedPeer)
	return multiless.New().Bool(
		me.p.Trusted, other.p.Trusted,
	).Uint32(
		me.prio, other.prio,
	).Less()
}

func newPeerPool(n int, prio func(Peer) peerPriority) peerPool {
	return peerPool{
		m:       &sync.RWMutex{},
		om:      btree.New(n),
		getPrio: prio,
	}
}

type peerPool struct {
	m       *sync.RWMutex
	om      *btree.BTree
	getPrio func(Peer) peerPriority
}

func (t *peerPool) Each(f func(Peer)) {
	t.m.RLock()
	defer t.m.RUnlock()

	t.om.Ascend(func(i btree.Item) bool {
		f(i.(prioritizedPeer).p)
		return true
	})
}

func (t *peerPool) Len() int {
	t.m.RLock()
	defer t.m.RUnlock()
	return t.om.Len()
}

// Returns true if a peer is replaced.
func (t *peerPool) Add(p Peer) bool {
	t.m.Lock()
	defer t.m.Unlock()
	prio := t.getPrio(p)
	return t.om.ReplaceOrInsert(prioritizedPeer{prio, p}) != nil
}

func (t *peerPool) DeleteMin() (ret prioritizedPeer, ok bool) {
	t.m.Lock()
	defer t.m.Unlock()

	i := t.om.DeleteMin()
	if i == nil {
		return ret, false
	}

	return i.(prioritizedPeer), true
}

func (t *peerPool) PopMax() (p Peer, ok bool) {
	t.m.Lock()
	defer t.m.Unlock()

	i := t.om.DeleteMax()
	if i == nil {
		return p, false
	}

	return i.(prioritizedPeer).p, true
}
