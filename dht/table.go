package dht

import (
	"errors"
	"fmt"
	"sync"

	"github.com/james-lawrence/torrent/dht/int160"
)

func newTable(k int) *table {
	return &table{
		k: k,
		m: &sync.Mutex{},
	}
}

// Node table, with indexes on distance from root ID to bucket, and node addr.
type table struct {
	rootID  int160.T
	k       int
	m       *sync.Mutex
	buckets [160]bucket
	addrs   map[string]map[int160.T]struct{}
}

func (tbl *table) K() int {
	return tbl.k
}

func (tbl *table) randomIdForBucket(bucketIndex int) int160.T {
	randomId := randomIdInBucket(tbl.rootID, bucketIndex)
	if randomIdBucketIndex := tbl.bucketIndex(randomId); randomIdBucketIndex != bucketIndex {
		panic(fmt.Sprintf("bucket index for random id %v == %v not %v", randomId, randomIdBucketIndex, bucketIndex))
	}
	return randomId
}

func (tbl *table) dropNode(n *node) {
	as := n.Addr.String()
	if _, ok := tbl.addrs[as][n.Id]; !ok {
		panic("missing id for addr")
	}
	delete(tbl.addrs[as], n.Id)
	if len(tbl.addrs[as]) == 0 {
		delete(tbl.addrs, as)
	}
	b := tbl.bucketForID(n.Id)

	b.Remove(n)
}

func (tbl *table) bucketForID(id int160.T) *bucket {
	return &tbl.buckets[tbl.bucketIndex(id)]
}

func (tbl *table) numNodes() (num int) {
	for i := range tbl.buckets {
		num += tbl.buckets[i].Len()
	}
	return
}

func (tbl *table) bucketIndex(id int160.T) int {
	if id == tbl.rootID {
		panic("nobody puts the root ID in a bucket")
	}
	var a int160.T
	a.Xor(&tbl.rootID, &id)
	index := 160 - a.BitLen()
	return index
}

func (tbl *table) forNodes(f func(*node) bool) bool {
	for i := range tbl.buckets {
		if !tbl.buckets[i].EachNode(f) {
			return false
		}
	}
	return true
}

func (tbl *table) getNode(addr Addr, id int160.T) *node {
	if id == tbl.rootID {
		return nil
	}
	return tbl.buckets[tbl.bucketIndex(id)].GetNode(addr, id)
}

func (tbl *table) closestNodes(k int, target int160.T, filter func(*node) bool) (ret []*node) {
	tbl.m.Lock()
	defer tbl.m.Unlock()

	for bi := func() int {
		if target == tbl.rootID {
			return len(tbl.buckets) - 1
		} else {
			return tbl.bucketIndex(target)
		}
	}(); bi >= 0 && len(ret) < k; bi-- {
		for n := range tbl.buckets[bi].NodeIter() {
			if filter(n) {
				ret = append(ret, n)
			}
		}
	}
	// TODO: Keep only the closest.
	if len(ret) > k {
		ret = ret[:k]
	}
	return
}

func (tbl *table) addNode(n *node) error {
	if n.Id == tbl.rootID {
		return errors.New("is root id")
	}
	b := &tbl.buckets[tbl.bucketIndex(n.Id)]
	if b.GetNode(n.Addr, n.Id) != nil {
		return errors.New("already present")
	}
	if b.Len() >= tbl.k {
		return errors.New("bucket is full")
	}
	b.AddNode(n, tbl.k)

	tbl.m.Lock()
	defer tbl.m.Unlock()

	if tbl.addrs == nil {
		tbl.addrs = make(map[string]map[int160.T]struct{}, 160*tbl.k)
	}
	as := n.Addr.String()
	if tbl.addrs[as] == nil {
		tbl.addrs[as] = make(map[int160.T]struct{}, 1)
	}

	tbl.addrs[as][n.Id] = struct{}{}
	return nil
}
