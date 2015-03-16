package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log"
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

func (a *announceParams) parseUDP(buf []byte) (err error) {
	var (
		announce UDPAnnounce
	)
	buffer := bytes.NewReader(buf[:98])
	err = binary.Read(buffer, binary.BigEndian, &announce)
	a.infoHash = fmt.Sprintf("%s", announce.Info_hash)
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
	a.peerID = fmt.Sprintf("%s", announce.Peer_id)
	a.port = int(announce.Port)
	a.downloaded = announce.Downloaded
	a.uploaded = announce.Uploaded
	a.left = announce.Left
	a.numWant = int(announce.Num_want)
	log.Printf("%s", announce.Info_hash)
	return
}
func handleUDPPacket(conn *net.UDPConn, buf []byte, remote *net.UDPAddr) (err error) {
	log.Println(buf)
	var (
		header UDPHeader
	)
	var params announceParams

	buffer := bytes.NewReader(buf[:16])
	err = binary.Read(buffer, binary.BigEndian, &header)
	if err != nil {
		return
	}

	log.Printf("%+v", header)
	switch header.Action {
	case 0: //connect
		b := new(bytes.Buffer)
		resp := UDPConnectResponce{Action: header.Action, Transaction_id: header.Transaction_id, Connection_id: header.Connection_id}
		err := binary.Write(b, binary.BigEndian, resp)
		if err == nil {
			log.Println(b.Bytes())
			conn.WriteTo(b.Bytes(), remote)
		}
	case 1: //announce
		err = params.parseUDP(buf)
		log.Printf("%+v", params)
	case 2: //scrape
	}
	return
	//conn.WriteTo(buf, remote)
}
