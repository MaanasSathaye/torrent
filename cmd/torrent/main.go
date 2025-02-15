// Downloads torrents from the command-line.
package main

import (
	"context"
	"expvar"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/anacrolix/missinggo/v2"
	"golang.org/x/xerrors"

	"github.com/anacrolix/tagflag"
	humanize "github.com/dustin/go-humanize"
	"github.com/gosuri/uiprogress"
	"golang.org/x/time/rate"

	"github.com/james-lawrence/torrent"
	"github.com/james-lawrence/torrent/autobind"
	"github.com/james-lawrence/torrent/internal/x/debugx"
	"github.com/james-lawrence/torrent/iplist"
	"github.com/james-lawrence/torrent/metainfo"
	"github.com/james-lawrence/torrent/storage"
)

var progress = uiprogress.New()

func torrentBar(t torrent.Torrent) {
	bar := progress.AddBar(1)
	bar.AppendCompleted()
	bar.AppendFunc(func(*uiprogress.Bar) (ret string) {
		select {
		case <-t.GotInfo():
		default:
			return "getting info"
		}

		stats := t.Stats()

		if stats.Seeding {
			return "seeding"
		}

		if t.BytesCompleted() == t.Info().TotalLength() {
			return "completed"
		}

		return fmt.Sprintf(
			"(%d) downloading (%s/%s) peers(%d/%d) completed(%d/%d) missing(%d) outstanding(%d) unverified(%d)",
			os.Getpid(),
			humanize.Bytes(uint64(t.BytesCompleted())),
			humanize.Bytes(uint64(t.Info().TotalLength())),
			stats.ActivePeers,
			stats.MaximumAllowedPeers,
			stats.Completed,
			t.Info().NumPieces(),
			stats.Missing,
			stats.Outstanding,
			stats.Unverified,
		)
	})
	bar.PrependFunc(func(*uiprogress.Bar) string {
		return t.Name()
	})
	go func() {
		<-t.GotInfo()
		tl := int(t.Info().TotalLength())
		if tl == 0 {
			bar.Set(1)
			return
		}
		bar.Total = tl
		for {
			bc := t.BytesCompleted()
			bar.Set(int(bc))
			time.Sleep(time.Second)
		}
	}()
}

func addTorrents(store storage.ClientImpl, client *torrent.Client) error {
	for _, arg := range flags.Torrent {
		t, err := func() (torrent.Torrent, error) {
			if strings.HasPrefix(arg, "magnet:") {
				t, _, err := client.MaybeStart(torrent.NewFromMagnet(arg, torrent.OptionStorage(store)))
				if err != nil {
					return nil, xerrors.Errorf("error adding magnet: %w", err)
				}
				return t, nil
			} else if strings.HasPrefix(arg, "http://") || strings.HasPrefix(arg, "https://") {
				response, err := http.Get(arg)
				if err != nil {
					return nil, xerrors.Errorf("Error downloading torrent file: %s", err)
				}

				metaInfo, err := metainfo.Load(response.Body)
				defer response.Body.Close()
				if err != nil {
					return nil, xerrors.Errorf("error loading torrent file %q: %s\n", arg, err)
				}
				t, _, err := client.MaybeStart(torrent.NewFromMetaInfo(metaInfo, torrent.OptionStorage(store)))
				if err != nil {
					return nil, xerrors.Errorf("adding torrent: %w", err)
				}
				return t, nil
			} else if strings.HasPrefix(arg, "infohash:") {
				t, _, err := client.MaybeStart(torrent.New(metainfo.NewHashFromHex(strings.TrimPrefix(arg, "infohash:"))))
				return t, err
			}

			metaInfo, err := metainfo.LoadFromFile(arg)
			if err != nil {
				return nil, xerrors.Errorf("error loading torrent file %q: %s\n", arg, err)
			}
			t, _, err := client.MaybeStart(torrent.NewFromMetaInfo(metaInfo))
			if err != nil {
				return nil, xerrors.Errorf("adding torrent: %w", err)
			}
			return t, nil
		}()
		if err != nil {
			return xerrors.Errorf("adding torrent for %q: %w", arg, err)
		}
		torrentBar(t)
		t.Tune(torrent.TunePeers(func() (ret []torrent.Peer) {
			for _, ta := range flags.TestPeer {
				ret = append(ret, torrent.Peer{
					IP:   ta.IP,
					Port: ta.Port,
				})
			}
			return
		}()...))

		go func() {
			<-t.GotInfo()
			t.DownloadAll()
		}()
	}
	return nil
}

var flags = struct {
	Mmap            bool           `help:"memory-map torrent data"`
	TestPeer        []*net.TCPAddr `help:"addresses of some starting peers"`
	Seed            bool           `help:"seed after download is complete"`
	Addr            string         `help:"network listen addr"`
	UploadRate      tagflag.Bytes  `help:"max piece bytes to send per second"`
	DownloadRate    tagflag.Bytes  `help:"max bytes per second down from peers"`
	CPU             bool           `help:"enable cpu profile"`
	Debug           bool
	PackedBlocklist string
	Stats           *bool
	PublicIP        net.IP
	Progress        bool
	Quiet           bool `help:"discard client logging"`
	Dht             bool
	tagflag.StartPos
	Torrent []string `arity:"+" help:"torrent file path or magnet uri"`
}{
	UploadRate:   -1,
	DownloadRate: -1,
	Progress:     true,
	Dht:          true,
}

func stdoutAndStderrAreSameFile() bool {
	fi1, _ := os.Stdout.Stat()
	fi2, _ := os.Stderr.Stat()
	return os.SameFile(fi1, fi2)
}

func statsEnabled() bool {
	if flags.Stats == nil {
		return flags.Debug
	}
	return *flags.Stats
}

func exitSignalHandlers(notify *missinggo.SynchronizedEvent) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	for {
		log.Printf("close signal received: %+v\n", <-c)
		notify.Set()
	}
}

func main() {
	go debugx.DumpOnSignal(context.Background(), syscall.SIGUSR2)
	if err := mainErr(); err != nil {
		log.Printf("error in main: %v\n", err)
		os.Exit(1)
	}
}

func mainErr() error {
	log.SetFlags(log.Flags() | log.Lshortfile)
	tagflag.Parse(&flags)
	if stdoutAndStderrAreSameFile() {
		log.SetOutput(progress.Bypass())
	}

	store := storage.NewFile("")
	autobinder := autobind.New()
	clientConfig := torrent.NewDefaultClientConfig()
	clientConfig.DisableAcceptRateLimiting = true
	clientConfig.NoDHT = !flags.Dht
	clientConfig.Seed = flags.Seed
	clientConfig.PublicIP4 = flags.PublicIP
	clientConfig.PublicIP6 = flags.PublicIP
	if flags.PackedBlocklist != "" {
		blocklist, err := iplist.MMapPackedFile(flags.PackedBlocklist)
		if err != nil {
			return xerrors.Errorf("loading blocklist: %v", err)
		}
		defer blocklist.Close()
		// clientConfig.IPBlocklist = blocklist
	}
	if flags.Mmap {
		store = storage.NewMMap("")
	}
	if flags.Addr != "" {
		autobinder = autobind.NewSpecified(flags.Addr)
	}
	if flags.UploadRate != -1 {
		clientConfig.UploadRateLimiter = rate.NewLimiter(rate.Limit(flags.UploadRate), 256<<10)
	}
	if flags.DownloadRate != -1 {
		clientConfig.DownloadRateLimiter = rate.NewLimiter(rate.Limit(flags.DownloadRate), 1<<20)
	}

	if flags.Debug && !flags.Quiet {
		clientConfig.Debug = log.New(os.Stderr, "", log.Flags())
	}

	if flags.Quiet {
		clientConfig.Logger = log.New(ioutil.Discard, "", log.Flags())
	}

	var stop missinggo.SynchronizedEvent
	defer func() {
		stop.Set()
	}()

	client, err := autobinder.Bind(torrent.NewClient(clientConfig))
	if err != nil {
		return xerrors.Errorf("creating client: %v", err)
	}
	defer client.Close()
	go exitSignalHandlers(&stop)
	go func() {
		<-stop.C()
		client.Close()
	}()

	// Write status on the root path on the default HTTP muxer. This will be bound to localhost
	// somewhere if GOPPROF is set, thanks to the envpprof import.
	http.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		client.WriteStatus(w)
	})
	if flags.Progress {
		progress.Start()
	}
	addTorrents(store, client)
	if client.WaitAll() {
		log.Print("downloaded ALL the torrents")
	} else {
		return xerrors.New("y u no complete torrents?!")
	}
	if flags.Seed {
		outputStats(client)
		<-stop.C()
	}
	outputStats(client)
	return nil
}

func outputStats(cl *torrent.Client) {
	if !statsEnabled() {
		return
	}
	expvar.Do(func(kv expvar.KeyValue) {
		fmt.Printf("%s: %s\n", kv.Key, kv.Value)
	})
	cl.WriteStatus(os.Stdout)
}
