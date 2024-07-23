// Copyright 2024 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package traceroute

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log"
	"math/rand"
	"net"
	"time"

	"golang.org/x/net/ipv6"
)

func (t *Trace) SendTracesTCP6() {
	sport := uint16(1000 + t.PortOffset + rand.Int31n(500))
	fmt.Println(t.srcIP.String())
	conn, err := net.ListenPacket("ip6:tcp", t.srcIP.String())
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	rSocket := ipv6.NewPacketConn(conn)
	rSocket.SetChecksum(true, 0x10)

	seq := uint32(1000)
	mod := uint32(1 << 30)
	for ttl := 1; ttl <= int(t.MaxHops); ttl++ {
		for j := 0; j < t.TracesPerHop; j++ {
			cm, payload := t.BuildTCP6SYNPkt(sport, t.destPort, uint16(ttl), seq, 0)
			rSocket.WriteTo(payload, cm, &net.IPAddr{IP: t.destIP})
			pb := &Probe{
				id:       seq,
				dest:     t.destIP,
				ttl:      ttl,
				sendtime: time.Now(),
			}
			t.SendChan <- pb
			seq = (seq + 4) % mod
			go t.ReceiveTracesTCP6ICMP()
			go t.ReceiveTracesTCP6()
			time.Sleep(time.Microsecond * time.Duration(200000/t.PacketRate))
		}
	}
}

func (t *Trace) ReceiveTracesTCP6() {
	recvTCPConn, err := net.ListenIP("ip6:tcp", &net.IPAddr{IP: t.srcIP})
	if err != nil {
		log.Fatal("bind TCP failure:", err)
	}
	buf := make([]byte, 1500)
	n, raddr, err := recvTCPConn.ReadFrom(buf)
	if err != nil {
		return
	}

	tcphdr, _ := parseTCP(buf)
	if (n >= 20) && (n <= 100) {
		if (tcphdr.Flags == TCP_ACK+TCP_SYN) && (raddr.String() == t.destIP.String()) {
			pb := &Probe{
				id:       tcphdr.AckNum - 1,
				saddr:    net.ParseIP(raddr.String()),
				recvTime: time.Now(),
			}
			t.ReceiveChan <- pb
		}
	}
}

func (t *Trace) ReceiveTracesTCP6ICMP() {
	//laddr := &net.IPAddr{IP: t.srcIP}
	recvICMPConn, err := net.ListenIP("ip6:ipv6-icmp", &net.IPAddr{IP: t.srcIP})
	if err != nil {
		log.Fatal("bind failure:", err)
	}
	for {
		buf := make([]byte, 1500)
		n, raddr, err := recvICMPConn.ReadFrom(buf)
		if err != nil {
			break
		}

		icmpType := buf[0]
		if (icmpType == 1 || (icmpType == 3 && buf[1] == 0)) && (n >= 36) { //TTL Exceeded or Port Unreachable
			ipv6hdr, _ := ipv6.ParseHeader(buf[8:])
			tcphdr, _ := parseTCP(buf[8+ipv6.HeaderLen : 48+ipv6.HeaderLen])
			if ipv6hdr.Dst.Equal(t.destIP) { // && dstPort == t.dstPort {
				pb := &Probe{
					id:       tcphdr.SeqNum,
					saddr:    net.ParseIP(raddr.String()),
					recvTime: time.Now(),
				}
				t.ReceiveChan <- pb
			}
		}
	}
}

func (t *Trace) IPv6TCPProbe(dport uint16) {
	seq := uint32(1000)
	mod := uint32(1 << 30)
	for i := 0; i < t.MaxHops; i++ {
		go t.IPv6TCPPing(seq, dport)
		seq = (seq + 4) % mod
		time.Sleep(time.Microsecond * time.Duration(200000/t.PacketRate))
	}
}

func (t *Trace) IPv6TCPPing(seq uint32, dport uint16) {
	pbs := &Probe{
		id:       seq,
		dest:     t.destIP,
		ttl:      0,
		sendtime: time.Now(),
	}
	t.SendChan <- pbs

	conn, err := net.DialTimeout("ip6:tcp", fmt.Sprintf("%s:%d", t.destIP.String(), dport), time.Second*2)
	if err != nil {
		return
	}
	conn.Close()

	fmt.Println("tcp probe")
	pbr := &Probe{
		id:       seq,
		saddr:    t.destIP,
		recvTime: time.Now(),
	}
	t.ReceiveChan <- pbr
}

func (t *Trace) BuildTCP6SYNPkt(sport, dport, ttl uint16, seq uint32, tc int) (*ipv6.ControlMessage, []byte) {
	cm := &ipv6.ControlMessage{
		HopLimit: int(ttl),
	}

	psyHdr := pseudoHeaderTCPIPv6(t.srcIP, t.destIP)

	tcp := TCPHeader{
		Src:        sport,
		Dst:        dport,
		SeqNum:     seq,
		AckNum:     0,
		DataOffset: 160,
		Flags:      TCP_SYN,
		Window:     64240,
		Urgent:     0,
	}

	//payload is TCP Optionheader
	payload := []byte{0x02, 0x04, 0x05, 0xb4, 0x04, 0x02, 0x08, 0x0a, 0x7f, 0x73, 0xf9, 0x3a, 0x00, 0x00, 0x00, 0x00, 0x01, 0x03, 0x03, 0x07}

	var b bytes.Buffer
	binary.Write(&b, binary.BigEndian, &tcp)
	binary.Write(&b, binary.BigEndian, &psyHdr)
	binary.Write(&b, binary.BigEndian, &payload)

	tcp.Checksum = checkSum(b.Bytes())

	var ret bytes.Buffer
	binary.Write(&ret, binary.BigEndian, &tcp)
	binary.Write(&ret, binary.BigEndian, &payload)

	return cm, ret.Bytes()
}

func pseudoHeaderTCPIPv6(src, dst net.IP) []byte {
	const ProtocolTCP = 6 // Transmission Control
	const ipv6PseudoHeaderLen = 2*net.IPv6len + 8
	b := make([]byte, ipv6PseudoHeaderLen)
	copy(b, src)
	copy(b[net.IPv6len:], dst)
	b[len(b)-1] = byte(ProtocolTCP)
	return b
}
