package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
)

type UDPHeader struct {
	Connection_id  uint64
	Action         uint32
	Transaction_id uint32
}
type UDPConnectResponce struct {
	Action         uint32
	Transaction_id uint32
	Connection_id  uint64
}

type UDPIP struct {
	Ip   uint32
	Port uint16
}
type UDPAnnounceResponce struct {
	Action         uint32
	Transaction_id uint32
	Interval       uint32
	Seeders        uint32
	Leechers       uint32
}
type UDPAnnounce struct {
	UDPHeader
	Info_hash  [20]byte
	Peer_id    [20]byte
	Downloaded uint64
	Left       uint64
	Uploaded   uint64
	Event      uint32
	Ipaddress  int32
	Key        uint32
	Num_want   int32
	Port       uint16
}
type UDPScrape struct {
	UDPHeader
	Info_hash [20]byte
}

func (t Torrents) ProcessUDP(params *announceParams, b *bytes.Buffer) (err error) {
	t.Append(params)

	count := uint32(t.PeersCount(params.TorrentID))
	resp := UDPAnnounceResponce{Action: 1, Transaction_id: params.transaction_id, Interval: 60, Seeders: count, Leechers: count}
	err = binary.Write(b, binary.BigEndian, resp)
	peers, err := t.getPeersU(params)
	if params.event != "stopped" {
		for _, p := range peers {
			err = binary.Write(b, binary.BigEndian, p)
		}
	}
	return
}
func (a *announceParams) parseUDP(buf []byte, remote *net.UDPAddr) (err error) {
	var (
		announce UDPAnnounce
	)
	buffer := bytes.NewReader(buf[:98])
	err = binary.Read(buffer, binary.BigEndian, &announce)
	a.TorrentID = TorrentID(fmt.Sprintf("%s", announce.Info_hash))
	switch announce.Event {
	case 0:
		a.event = "none"
	case 1:
		a.event = "completed"
	case 2:
		a.event = "started"
	case 3:
		a.event = "stopped"
	}
	addr := &net.IPAddr{IP: remote.IP}
	a.ip = addr
	a.peerID = PeerID(fmt.Sprintf("%s", announce.Peer_id))
	a.port = int(announce.Port)
	a.downloaded = announce.Downloaded
	a.uploaded = announce.Uploaded
	a.left = announce.Left
	a.numWant = int(announce.Num_want)
	a.connection_id = announce.Connection_id
	a.transaction_id = announce.Transaction_id
	return
}
func inet_aton(ip *net.IPAddr) uint32 {
	ip_byte := ip.IP.To4()
	return uint32(ip_byte[0])<<24 + uint32(ip_byte[1])<<16 + uint32(ip_byte[2])<<8 + uint32(ip_byte[3])
}
func handleUDPPacket(conn *net.UDPConn, buf []byte, remote *net.UDPAddr) (err error) {
	var (
		header UDPHeader
		scrape UDPScrape
	)
	var params announceParams
	buffer := bytes.NewReader(buf[:16])
	err = binary.Read(buffer, binary.BigEndian, &header)
	if err != nil {
		return
	}

	switch header.Action {
	case 0: //connect
		b := new(bytes.Buffer)
		resp := UDPConnectResponce{Action: header.Action, Transaction_id: header.Transaction_id, Connection_id: header.Connection_id}
		err := binary.Write(b, binary.BigEndian, resp)
		if err == nil {
			conn.WriteTo(b.Bytes(), remote)
		}
	case 1: //announce
		var b bytes.Buffer
		err = params.parseUDP(buf, remote)
		logger.Debugf("%+v\n", params)
		err = torrents.ProcessUDP(&params, &b)
		if err == nil {
			conn.WriteTo(b.Bytes(), remote)
		}
	case 2: //scrape
		err = binary.Read(buffer, binary.BigEndian, &scrape)
		if err != nil {
			logger.Debugln(err)
		} else {
			logger.Debugf("Scrape request: %+v", scrape)
		}

	}
	return

}
