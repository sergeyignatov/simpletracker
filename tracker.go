package main

import (
	"bytes"

	b64 "encoding/base64"
	"flag"
	"fmt"
	bencode "github.com/jackpal/bencode-go"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
	"net/url"
	"strconv"
	"sync"
	"time"
)

type Peer struct {
	ip         *net.IPAddr
	port       int
	id         string
	lastSeen   time.Time
	uploaded   uint64
	downloaded uint64
	left       uint64
}
type logLevel int

var logger logLevel

const (
	LOG_NORMAL logLevel = iota
	LOG_DEBUG
	LOG_TRACE
)

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
	infoHash       string
	peerID         string
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

type Torrent struct {
	peers Peers
}
type Torrents struct {
	t map[string]*Torrent
	sync.RWMutex
}

type bmap map[string]interface{}

type Peers struct {
	t map[string]*Peer
	sync.RWMutex
}

func (t *Torrents) Delete(id string) {
	t.Lock()
	defer t.Unlock()
	delete(t.t, id)
}

func (t *Torrents) PeersCount(id string) int {
	if pp, ok := t.t[id]; ok {
		return pp.peers.Count()
	}
	return 0
}

func (p *Peers) Delete(id string) {
	p.Lock()
	defer p.Unlock()
	delete(p.t, id)
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

func NewTorrent() Torrents {
	return Torrents{t: make(map[string]*Torrent)}
}

var torrents = NewTorrent()

func (t Torrents) Append(params *announceParams) {
	if tt, ok := t.t[params.infoHash]; ok {
		tt.peers.Add(params)

		if params.event == "stopped" {
			tt.peers.Delete(params.peerID)
			if tt.peers.Count() == 0 {
				t.Delete(params.infoHash)
			}
		}
	} else {
		if params.event != "stopped" {
			peers := Peers{t: make(map[string]*Peer)}
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
			t.t[params.infoHash] = &Torrent{peers: peers}
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
func (t *Torrents) String() (out string) {
	t.RLock()
	defer t.RUnlock()

	for k, tt := range t.t {
		for _, pp := range tt.peers.t {
			out += fmt.Sprintf("%s - %s:%d (downloaded: %5d M, uploaded: %5d M)\n",
				b64.StdEncoding.EncodeToString([]byte(k)), pp.ip, pp.port, pp.downloaded/1024/1024, pp.uploaded/1024/1024)
		}
	}
	return
}

func (t *Torrents) getPeersU(params *announceParams) (peers []UDPIP, err error) {
	if tt, ok := t.t[params.infoHash]; ok {
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
	if tt, ok := t.t[params.infoHash]; ok {
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
	if tt, ok := t.t[params.infoHash]; ok {
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
	a.infoHash = q.Get("info_hash")
	if a.infoHash == "" {
		err = fmt.Errorf("Missing info_hash")
		return
	}

	a.peerID = q.Get("peer_id")
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

func main() {
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
	logger.Logln("SimpleTracker started")
	log.Fatal(http.ListenAndServe(fmt.Sprintf("%s:%d", *bind, *httpPort), nil))

}
