package main

import (
	"bytes"

	"flag"
	"fmt"
	bencode "github.com/jackpal/bencode-go"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

type Peer struct {
	ip         string
	port       int
	id         string
	lastSeen   time.Time
	uploaded   uint64
	downloaded uint64
	left       uint64
}

type announceParams struct {
	infoHash          string
	peerID            string
	ip                string // optional
	port              int
	uploaded          uint64
	downloaded        uint64
	left              uint64
	compact           bool
	noPeerID          bool
	event             string
	numWant           int
	trackerID         string
	peerListenAddress *net.TCPAddr
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
	log.Println("Torrent delete", id)
	delete(t.t, id)
}

func (p *Peers) Delete(id string) {
	p.Lock()
	defer p.Unlock()
	log.Println("Peers delete", id)
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
			listenAddr: params.peerListenAddress,
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

func (t Torrents) Process(params *announceParams, response bmap) (err error) {
	if tt, ok := t.t[params.infoHash]; ok {
		tt.peers.Add(params)
		/*if pp, ok := tt.peers.t[params.peerID]; ok {
			pp.lastSeen = time.Now()
			pp.downloaded = params.downloaded
			pp.uploaded = params.uploaded
			pp.left = params.left
			tt.peers.t[params.peerID] = pp
		} else {

			peer := &Peer{
				id:         params.peerID,
				listenAddr: peerListenAddress,
				lastSeen:   time.Now(),
				uploaded:   params.uploaded,
				downloaded: params.downloaded,
				left:       params.left,
			}
			tt.peers.Add(params)

		}*/
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
				listenAddr: params.peerListenAddress,
				lastSeen:   time.Now(),
				uploaded:   params.uploaded,
				downloaded: params.downloaded,
				left:       params.left,
			}
			peers.t[params.peerID] = peer
			t.t[params.infoHash] = &Torrent{peers: peers}
		}
	}
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
func (t *Torrents) getPeers(params *announceParams) (peers []bmap, err error) {
	if tt, ok := t.t[params.infoHash]; ok {
		tt.peers.RLock()
		defer tt.peers.RUnlock()
		for k, p := range tt.peers.t {
			if k == params.peerID {
				continue
			}
			la := p.listenAddr
			var peer bmap = make(bmap)
			if !params.noPeerID {
				peer["peer id"] = p.id
			}
			peer["ip"] = la.IP.String()
			peer["port"] = strconv.Itoa(la.Port)
			peers = append(peers, peer)
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

			la := p.listenAddr
			ip4 := la.IP.To4()
			if ip4 == nil {
				err = fmt.Errorf("Can't write a compact peer for a non-IPv4 peer %v", p.listenAddr.String())
				return
			}
			_, err = b.Write(ip4)
			if err != nil {
				return
			}
			port := la.Port
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
	a.ip = q.Get("ip")
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
func newTrackerPeerListenAddress(requestRemoteAddr string, params *announceParams) (addr *net.TCPAddr, err error) {
	var host string
	if params.ip != "" {
		host = params.ip
	} else {
		host, _, err = net.SplitHostPort(requestRemoteAddr)
		if err != nil {
			return
		}
	}
	return net.ResolveTCPAddr("tcp", net.JoinHostPort(host, strconv.Itoa(params.port)))
}
func rootHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "OK")
}
func announceHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	response := make(bmap)
	var params announceParams
	//var peerListenAddress *net.TCPAddr
	err := params.parse(r.URL)
	var b bytes.Buffer
	peerListenAddress, err := newTrackerPeerListenAddress(r.RemoteAddr, &params)
	params.peerListenAddress = peerListenAddress

	_ = torrents.Process(&params, response)
	//log.Printf("%+v", params)
	/*for _, v := range torrents.t {
		for _, v1 := range v.peers.t {
			log.Printf("%+v", v1)
		}

	}*/
	//log.Printf("%+v", torrents)

	if err != nil {
		log.Printf("announce from %v failed: %#v", r.RemoteAddr, err.Error())
		errorResponse := make(bmap)
		errorResponse["failure reason"] = err.Error()
		err = bencode.Marshal(&b, errorResponse)
	} else {
		err = bencode.Marshal(&b, response)
	}
	if err == nil {
		w.Write(b.Bytes())
	}
}

func main() {
	udpPort := flag.Int("udpport", 6969, "Listen UDP port")
	httpPort := flag.Int("httpport", 6969, "Listen HTTP port")

	http.HandleFunc("/", rootHandler)
	http.HandleFunc("/announce", announceHandler)
	addr, _ := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", *udpPort))
	sock, err := net.ListenUDP("udp", addr)
	defer sock.Close()
	if err != nil {
		log.Fatal(err)
	}
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
	log.Println("SimpleTracker started")
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", *httpPort), nil))

}
