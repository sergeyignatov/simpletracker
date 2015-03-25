package main

import (
	"bytes"

	//b64 "encoding/base64"
	"flag"
	"fmt"
	bencode "github.com/jackpal/bencode-go"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
	"net/url"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"
)

type Peer struct {
	ip         *net.IPAddr
	port       int
	id         PeerID
	lastSeen   time.Time
	uploaded   uint64
	downloaded uint64
	left       uint64
	completed  bool
}
type logLevel int

var logger logLevel

const MAX_ANNOUNCE_INTERVAL = 2000
const (
	NONE int = iota
	COMPLETED
	STARTED
	STOPPED
)

const (
	LOG_NORMAL logLevel = iota
	LOG_DEBUG
	LOG_TRACE
)
const VERSION = "0.3.3"

func (ll logLevel) Debugf(format string, a ...interface{}) {
	if ll >= LOG_DEBUG {
		log.Printf(format, a...)
	}
}

func (ll logLevel) Debugln(a ...interface{}) {
	if ll >= LOG_DEBUG {
		log.Println(a...)
	}
}

func (ll logLevel) Tracef(format string, a ...interface{}) {
	if ll >= LOG_TRACE {
		log.Printf(format, a...)
	}
}

func (ll logLevel) Traceln(a ...interface{}) {
	if ll >= LOG_TRACE {
		log.Println(a...)
	}
}
func (ll logLevel) Logln(a ...interface{}) {
	log.Println(a...)
}

func (ll logLevel) Logf(format string, a ...interface{}) {
	log.Printf(format, a...)
}

type announceParams struct {
	TorrentID      TorrentID
	peerID         PeerID
	ip             *net.IPAddr
	port           int
	uploaded       uint64
	downloaded     uint64
	left           uint64
	compact        bool
	noPeerID       bool
	event          string
	numWant        int
	trackerID      string
	connection_id  uint64
	transaction_id uint32
}
type TorrentID string
type PeerID string
type TorrentIDs []TorrentID

func (s TorrentIDs) Len() int {
	return len(s)
}
func (s TorrentIDs) Less(i, j int) bool {
	return s[i] < s[j]
}

func (s TorrentIDs) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (i TorrentID) String() string {
	return string(i)
}

type Torrent struct {
	added time.Time
	peers Peers
}
type Torrents struct {
	t map[TorrentID]*Torrent
	sync.RWMutex
}

type bmap map[string]interface{}

type Peers struct {
	t map[PeerID]*Peer
	sync.RWMutex
}

func (t *Torrents) Delete(id TorrentID) {
	t.Lock()
	defer t.Unlock()
	delete(t.t, id)
}

func (t *Torrents) CleanUp() {
	for tid, tt := range t.t {
		for pid, p := range tt.peers.t {
			if time.Now().Sub(p.lastSeen) > time.Duration(MAX_ANNOUNCE_INTERVAL)*time.Second {
				logger.Debugln("Wiped due to timeout")
				tt.peers.Delete(pid, tid)
			}
		}
	}
}

func (t *Torrents) PeersCount(id TorrentID) int {
	if pp, ok := t.t[id]; ok {
		return pp.peers.Count()
	}
	return 0
}

func (p *Peers) Delete(id PeerID, TorrentID TorrentID) {
	p.Lock()
	delete(p.t, id)
	p.Unlock()
	if p.Count() == 0 {
		torrents.Delete(TorrentID)
	}
}
func (p *Peers) setComplete(id PeerID) {
	p.Lock()
	defer p.Unlock()
	p.t[id].completed = true
}

func (p *Peers) Count() int {
	p.RLock()
	defer p.RUnlock()
	return len(p.t)
}

func (p *Peers) Add(params *announceParams) {
	p.Lock()
	defer p.Unlock()
	if pp, ok := p.t[params.peerID]; ok {
		pp.lastSeen = time.Now()
		pp.downloaded = params.downloaded
		pp.uploaded = params.uploaded
		pp.left = params.left
		p.t[params.peerID] = pp
	} else {
		peer := &Peer{
			id:         params.peerID,
			ip:         params.ip,
			port:       params.port,
			lastSeen:   time.Now(),
			uploaded:   params.uploaded,
			downloaded: params.downloaded,
			left:       params.left,
		}
		p.t[params.peerID] = peer
	}
}

func NewTorrents() Torrents {
	return Torrents{t: make(map[TorrentID]*Torrent)}
}
func NewTorrent(peers Peers) *Torrent {
	return &Torrent{added: time.Now(), peers: peers}
}

var torrents = NewTorrents()

func (t Torrents) Append(params *announceParams) {
	if tt, ok := t.t[params.TorrentID]; ok {
		tt.peers.Add(params)

		if params.left == 0 {
			tt.peers.setComplete(params.peerID)
		}
		switch params.event {
		case "stopped":
			tt.peers.Delete(params.peerID, params.TorrentID)
		case "completed":
			tt.peers.setComplete(params.peerID)
		}

	} else {
		if params.event != "stopped" {
			peers := Peers{t: make(map[PeerID]*Peer)}
			peer := &Peer{
				id:         params.peerID,
				ip:         params.ip,
				port:       params.port,
				lastSeen:   time.Now(),
				uploaded:   params.uploaded,
				downloaded: params.downloaded,
				left:       params.left,
			}
			peers.t[params.peerID] = peer
			t.t[params.TorrentID] = NewTorrent(peers)

		}
	}

}

func (t Torrents) Process(params *announceParams, response bmap) (err error) {
	t.Append(params)
	response["interval"] = int64(60)
	if params.compact {
		var b bytes.Buffer
		err = t.writeCompactPeers(&b, params)
		if err != nil {
			return
		}
		response["peers"] = string(b.Bytes())

	} else {
		var peers []bmap
		peers, err = t.getPeers(params)
		if err != nil {
			return
		}
		response["peers"] = peers
	}
	return
}

type TS struct {
	id    TorrentID
	added time.Time
}
type ByAge []TS

func (a ByAge) Len() int           { return len(a) }
func (a ByAge) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByAge) Less(i, j int) bool { return a[i].added.Sub(a[j].added) > 0 }

func (t *Torrents) String() (out string) {
	t.RLock()
	defer t.RUnlock()
	tss := make([]TS, 0)
	for k, v := range t.t {
		tss = append(tss, TS{id: k, added: v.added})
	}
	sort.Sort(ByAge(tss))

	for _, k := range tss {
		out += fmt.Sprintf("%s\n", k.added.Format(time.Stamp))
		for _, pp := range t.t[k.id].peers.t {
			out += fmt.Sprintf("%32s:%d (downloaded: %5d MB, uploaded: %5d MB)\n",
				pp.ip, pp.port, (pp.downloaded / 1024 / 1024), (pp.uploaded / 1024 / 1024))
		}

	}
	return
}

func (t *Torrents) getPeersU(params *announceParams) (peers []UDPIP, err error) {
	if tt, ok := t.t[params.TorrentID]; ok {
		tt.peers.RLock()
		defer tt.peers.RUnlock()
		for _, p := range tt.peers.t {
			/*if k == params.peerID {
				continue
			}
			if params.ip == p.ip {
				continue
			}*/
			ppp := UDPIP{Ip: inet_aton(p.ip), Port: uint16(p.port)}
			peers = append(peers, ppp)
			logger.Debugf("Announce answer: %+v", ppp)
			if len(peers) == params.numWant {
				break
			}
		}
	}
	return
}

func (t *Torrents) getPeers(params *announceParams) (peers []bmap, err error) {
	if tt, ok := t.t[params.TorrentID]; ok {
		tt.peers.RLock()
		defer tt.peers.RUnlock()
		for k, p := range tt.peers.t {
			if k == params.peerID {
				continue
			}
			var peer bmap = make(bmap)
			if !params.noPeerID {
				peer["peer id"] = p.id
			}
			peer["ip"] = p.ip.String()
			peer["port"] = p.port
			peers = append(peers, peer)

			if len(peers) == params.numWant {
				break
			}
		}
	}
	return
}

func (t *Torrents) writeCompactPeers(b *bytes.Buffer, params *announceParams) (err error) {
	if tt, ok := t.t[params.TorrentID]; ok {
		tt.peers.RLock()
		defer tt.peers.RUnlock()
		for k, p := range tt.peers.t {
			if k == params.peerID {
				continue
			}

			ip4 := p.ip.IP.To4()
			if ip4 == nil {
				err = fmt.Errorf("Can't write a compact peer for a non-IPv4 peer %v", p.ip.String())
				return
			}
			_, err = b.Write(ip4)
			if err != nil {
				return
			}
			port := p.port
			portBytes := []byte{byte(port >> 8), byte(port)}
			_, err = b.Write(portBytes)
			if err != nil {
				return
			}
		}
	}
	return
}
func getUint64(v url.Values, key string) (i uint64, err error) {
	val := v.Get(key)
	if val == "" {
		err = fmt.Errorf("Missing query parameter: %v", key)
		return
	}
	return strconv.ParseUint(val, 10, 64)
}
func getBool(v url.Values, key string) (b bool, err error) {
	val := v.Get(key)
	if val == "" {
		err = fmt.Errorf("Missing query parameter: %v", key)
		return
	}
	return strconv.ParseBool(val)
}

func getUint(v url.Values, key string) (i int, err error) {
	var i64 uint64
	i64, err = getUint64(v, key)
	if err != nil {
		return
	}
	i = int(i64)
	return
}

func (a *announceParams) parse(u *url.URL) (err error) {
	q := u.Query()
	a.TorrentID = TorrentID(q.Get("info_hash"))
	if a.TorrentID == "" {
		err = fmt.Errorf("Missing info_hash")
		return
	}

	a.peerID = PeerID(q.Get("peer_id"))
	a.port, err = getUint(q, "port")
	if err != nil {
		return
	}
	a.uploaded, err = getUint64(q, "uploaded")
	if err != nil {
		return
	}
	a.downloaded, err = getUint64(q, "downloaded")
	if err != nil {
		return
	}
	a.left, err = getUint64(q, "left")
	if err != nil {
		return
	}
	if q.Get("compact") != "" {
		a.compact, err = getBool(q, "compact")
		if err != nil {
			return
		}
	}
	if q.Get("no_peer_id") != "" {
		a.noPeerID, err = getBool(q, "no_peer_id")
		if err != nil {
			return
		}
	}
	a.event = q.Get("event")
	if numWant := q.Get("numwant"); numWant != "" {
		a.numWant, err = strconv.Atoi(numWant)
		if err != nil {
			return
		}
	}
	a.trackerID = q.Get("trackerid")
	return
}

func rootHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprintf(w, torrents.String())
}

func announceHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	response := make(bmap)
	var params announceParams
	err := params.parse(r.URL)
	var b bytes.Buffer
	if params.ip == nil {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err == nil {
			addr, _ := net.ResolveIPAddr("ip", host)
			params.ip = addr
		}
	}
	logger.Debugf("%+v", params)

	if err != nil {
		log.Printf("announce from %v failed: %#v", r.RemoteAddr, err.Error())
		errorResponse := make(bmap)
		errorResponse["failure reason"] = err.Error()
		err = bencode.Marshal(&b, errorResponse)
	} else {
		err = torrents.Process(&params, response)
		err = bencode.Marshal(&b, response)
	}
	if err == nil {
		w.Write(b.Bytes())
	}
}
func usage() {
	fmt.Printf("Simple udp/http torrent tracker.\nVersion: %s\n\n", VERSION)
	flag.PrintDefaults()
	os.Exit(2)
}
func main() {
	flag.Usage = usage
	udpPort := flag.Int("udpport", 6969, "Listen UDP port")
	httpPort := flag.Int("httpport", 6969, "Listen HTTP port")
	verbose := flag.Bool("v", false, "enable verbose logging")
	debug := flag.Bool("vv", false, "enable more verbose (debug) logging")
	bind := flag.String("int", "0.0.0.0", "bind interface")
	flag.Parse()
	loglevel := LOG_NORMAL
	if *verbose {
		loglevel = LOG_DEBUG
	}
	if *debug {
		loglevel = LOG_TRACE
	}
	logger = logLevel(loglevel)
	http.HandleFunc("/", rootHandler)
	http.HandleFunc("/announce", announceHandler)
	addr, _ := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", *bind, *udpPort))
	sock, err := net.ListenUDP("udp", addr)
	if err != nil {
		log.Fatal(err)
	}

	defer sock.Close()
	go func() {
		buf := make([]byte, 1500)
		for {
			_, remote, err := sock.ReadFromUDP(buf)
			if err != nil {
				fmt.Println(err)
				continue
			}
			go handleUDPPacket(sock, buf, remote)
		}
	}()
	go func() {
		for {
			torrents.CleanUp()
			time.Sleep(60 * time.Second)
		}
	}()
	logger.Logf("SimpleTracker started on http://%s:%d, udp://%s:%d\n", *bind, *httpPort, *bind, *udpPort)
	log.Fatal(http.ListenAndServe(fmt.Sprintf("%s:%d", *bind, *httpPort), nil))

}
