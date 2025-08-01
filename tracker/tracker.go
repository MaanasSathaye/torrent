package tracker

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"net"
	"net/url"
	"time"

	"github.com/james-lawrence/torrent/dht/int160"
	"github.com/james-lawrence/torrent/dht/krpc"
	"github.com/james-lawrence/torrent/internal/langx"
	"github.com/james-lawrence/torrent/internal/netx"
)

// https://www.bittorrent.org/beps/bep_0015.html

const (
	None      AnnounceEvent = iota
	Completed               // The local peer just completed the torrent.
	Started                 // The local peer has just resumed this torrent.
	Stopped                 // The local peer is leaving the swarm.
)

type AnnounceOption func(*AnnounceRequest)

func AnnounceOptionKey(ar *AnnounceRequest) {
	ar.Key = int32(binary.BigEndian.Uint32(ar.PeerId[16:20]))
}

func AnnounceOptionUploaded(n int64) AnnounceOption {
	return func(ar *AnnounceRequest) {
		ar.Uploaded = n
	}
}

func AnnounceOptionDownloaded(n int64) AnnounceOption {
	return func(ar *AnnounceRequest) {
		ar.Downloaded = n
	}
}

func AnnounceOptionRemaining(n int64) AnnounceOption {
	return func(ar *AnnounceRequest) {
		ar.Left = n
	}
}

func AnnounceOptionEventStarted(ar *AnnounceRequest) {
	ar.Event = Started
}

func AnnounceOptionEventStopped(ar *AnnounceRequest) {
	ar.Event = Stopped
}

func AnnounceOptionEventCompleted(ar *AnnounceRequest) {
	ar.Event = Completed
}

func AnnounceOptionSeeding(ar *AnnounceRequest) {
	ar.Left = 0
}

func NewAccounceRequest(id int160.T, port int, hash int160.T, options ...AnnounceOption) AnnounceRequest {
	return langx.Clone(AnnounceRequest{
		PeerId:   int160.ByteArray(id),
		Port:     uint16(port),
		InfoHash: int160.ByteArray(hash),
		Event:    None,
		Left:     math.MaxInt64,
		NumWant:  -1,
	}, options...)
}

// Marshalled as binary by the UDP client, so be careful making changes.
type AnnounceRequest struct {
	InfoHash   [20]byte
	PeerId     [20]byte
	Downloaded int64
	Left       int64 // math.MaxInt64 will be used by default
	Uploaded   int64
	// Apparently this is optional. None can be used for announces done at
	// regular intervals.
	Event     AnnounceEvent
	IPAddress uint32
	Key       int32
	NumWant   int32 // How many peer addresses are desired. -1 for default.
	Port      uint16
} // 82 bytes

type AnnounceResponse struct {
	Interval int32 // Minimum seconds the local peer should wait before next announce.
	Leechers int32
	Seeders  int32
	Peers    []Peer
}

type AnnounceEvent int32

func (e AnnounceEvent) String() string {
	// See BEP 3, "event".
	return []string{"empty", "completed", "started", "stopped"}[e]
}

var (
	ErrBadScheme = errors.New("unknown scheme")
)

type Announce struct {
	TrackerUrl string
	// UdpNetwork string
	UserAgent string
	// If the port is zero, it's assumed to be the same as the Request.Port.
	ClientIp4 krpc.NodeAddr
	// If the port is zero, it's assumed to be the same as the Request.Port.
	ClientIp6 krpc.NodeAddr
	Dialer    netx.Dialer
}

func (me Announce) ForTracker(uri string) Announce {
	me.TrackerUrl = uri
	return me
}

func (me Announce) Do(ctx context.Context, req AnnounceRequest) (res AnnounceResponse, err error) {
	if me.Dialer == nil {
		me.Dialer = &net.Dialer{
			Timeout: 15 * time.Second,
		}
	}

	_url, err := url.Parse(me.TrackerUrl)
	if err != nil {
		return res, err
	}
	switch _url.Scheme {
	case "http", "https":
		return announceHTTP(ctx, _url, req, me)
	case "udp", "udp4", "udp6":
		return announceUDP(ctx, _url, req, me)
	default:
		return res, ErrBadScheme
	}
}
