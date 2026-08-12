// Package sim is a Meshtastic node simulator. It emits real length-delimited
// FromRadio protobuf frames (NodeInfo, Telemetry, Position) over a net.Conn in
// exactly the wire format the bridge decoder expects ([varint length][FromRadio]),
// so the full Faulty Link stack can run end-to-end without LoRa hardware.
package sim

import (
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	meshpb "github.com/peteedoo/faulty-link-backend/third_party/protobufs/meshtastic"
)

// Node is one virtual mesh node.
type Node struct {
	Num       uint32
	LongName  string
	ShortName string
	Battery   uint32
	Voltage   float32
	LatI      int32
	LonI      int32
	Alt       int32
}

// DefaultNodes returns the standard 3-node demo mesh:
// !000000a1 gateway, !000000a2 repeater, !000000a3 handset.
func DefaultNodes() []*Node {
	return []*Node{
		{Num: 0xa1, LongName: "Gateway 1", ShortName: "GW1", Battery: 100, Voltage: 4.10, LatI: 397000000, LonI: -1050000000, Alt: 1609},
		{Num: 0xa2, LongName: "Repeater 1", ShortName: "RP1", Battery: 87, Voltage: 3.95, LatI: 397100000, LonI: -1050100000, Alt: 1640},
		{Num: 0xa3, LongName: "Handset 1", ShortName: "HS1", Battery: 64, Voltage: 3.80, LatI: 397050000, LonI: -1050050000, Alt: 1615},
	}
}

// Serve streams frames to one connected client until it disconnects. If
// dropNode is non-zero and dropAfter > 0, that node stops being emitted after
// the delay — simulating an offline node.
func Serve(conn net.Conn, nodes []*Node, interval time.Duration, dropNode uint32, dropAfter time.Duration) {
	defer conn.Close()

	// Drain inbound bytes (the bridge sends ToRadio heartbeats) so the socket
	// never stalls; a read error means the client is gone.
	gone := make(chan struct{})
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := conn.Read(buf); err != nil {
				close(gone)
				return
			}
		}
	}()

	start := time.Now()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	emit := func() bool {
		dropped := dropNode != 0 && dropAfter > 0 && time.Since(start) >= dropAfter
		for _, n := range nodes {
			if dropped && n.Num == dropNode {
				continue
			}
			for _, f := range n.Frames() {
				if _, err := conn.Write(f); err != nil {
					return false
				}
			}
		}
		return true
	}

	if !emit() {
		return
	}
	for {
		select {
		case <-gone:
			return
		case <-ticker.C:
			if !emit() {
				return
			}
		}
	}
}

// Frames builds the NodeInfo, Telemetry, and Position FromRadio frames for a node.
func (n *Node) Frames() [][]byte {
	now := uint32(time.Now().Unix())

	nodeInfo := &meshpb.FromRadio{
		PayloadVariant: &meshpb.FromRadio_NodeInfo{
			NodeInfo: &meshpb.NodeInfo{
				Num:       n.Num,
				LastHeard: now,
				User: &meshpb.User{
					Id:        NodeID(n.Num),
					LongName:  n.LongName,
					ShortName: n.ShortName,
				},
			},
		},
	}

	battery := n.Battery
	voltage := n.Voltage
	telem := &meshpb.Telemetry{
		Time: now,
		Variant: &meshpb.Telemetry_DeviceMetrics{
			DeviceMetrics: &meshpb.DeviceMetrics{
				BatteryLevel: &battery,
				Voltage:      &voltage,
			},
		},
	}
	telemBytes, _ := proto.Marshal(telem)
	telemFrame := packetFrame(n.Num, meshpb.PortNum_TELEMETRY_APP, telemBytes)

	latI := n.LatI
	lonI := n.LonI
	alt := n.Alt
	pos := &meshpb.Position{Time: now, LatitudeI: &latI, LongitudeI: &lonI, Altitude: &alt}
	posBytes, _ := proto.Marshal(pos)
	posFrame := packetFrame(n.Num, meshpb.PortNum_POSITION_APP, posBytes)

	return [][]byte{frame(nodeInfo), frame(telemFrame), frame(posFrame)}
}

func packetFrame(from uint32, port meshpb.PortNum, payload []byte) *meshpb.FromRadio {
	return &meshpb.FromRadio{
		PayloadVariant: &meshpb.FromRadio_Packet{
			Packet: &meshpb.MeshPacket{
				From: from,
				PayloadVariant: &meshpb.MeshPacket_Decoded{
					Decoded: &meshpb.Data{Portnum: port, Payload: payload},
				},
			},
		},
	}
}

// frame marshals a FromRadio and prepends the Meshtastic stream header
// [START1][START2][len_hi][len_lo] (16-bit big-endian length).
func frame(m *meshpb.FromRadio) []byte {
	data, err := proto.Marshal(m)
	if err != nil {
		log.Printf("sim marshal: %v", err)
		return nil
	}
	out := make([]byte, 0, 4+len(data))
	out = append(out, 0x94, 0xc3, byte(len(data)>>8), byte(len(data)))
	return append(out, data...)
}

// NodeID formats a node number as the standard Meshtastic "!%08x" ID.
func NodeID(num uint32) string {
	s := strconv.FormatUint(uint64(num), 16)
	for len(s) < 8 {
		s = "0" + s
	}
	return "!" + s
}

// ParseNodeNum accepts "a3", "0xa3", or "!000000a3" and returns the numeric ID,
// or 0 if empty/invalid.
func ParseNodeNum(s string) uint32 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	s = strings.TrimPrefix(s, "!")
	s = strings.TrimPrefix(s, "0x")
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0
	}
	return uint32(v)
}
