// Package mesh handles Meshtastic TCP connectivity and telemetry ingestion.
package mesh

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"

	"google.golang.org/protobuf/proto"

	meshpb "github.com/peteedoo/faulty-link-backend/third_party/protobufs/meshtastic"
)

// Decoder reads length-delimited protobuf messages from an io.Reader.
// Meshtastic TCP framing: [varint length] [protobuf payload]
type Decoder struct {
	r      *bufio.Reader
	maxLen int
}

// ErrOversizedMessage is returned when a message exceeds the configured max length.
var ErrOversizedMessage = errors.New("message exceeds maximum allowed size")

// NewDecoder creates a new Decoder wrapping the provided reader.
func NewDecoder(r io.Reader, maxLen int) *Decoder {
	if maxLen <= 0 {
		maxLen = 1 << 20 // 1 MB default
	}
	return &Decoder{
		r:      bufio.NewReader(r),
		maxLen: maxLen,
	}
}

// Decode reads the next length-delimited message and returns the raw payload.
// This is the low-level framing layer; higher-level code will unmarshal protobuf.
func (d *Decoder) Decode() ([]byte, error) {
	// Read varint length prefix
	length, err := binary.ReadUvarint(d.r)
	if err != nil {
		return nil, fmt.Errorf("read length prefix: %w", err)
	}
	if length == 0 {
		return []byte{}, nil
	}
	if length > uint64(d.maxLen) {
		return nil, ErrOversizedMessage
	}

	// Read payload
	payload := make([]byte, length)
	_, err = io.ReadFull(d.r, payload)
	if err != nil {
		return nil, fmt.Errorf("read payload (%d bytes): %w", length, err)
	}
	return payload, nil
}

// DecodeMessage reads the next framed message and unmarshals it into a Message.
// It parses the Meshtastic FromRadio protobuf and extracts NodeInfo, Telemetry,
// or Position depending on the payload variant.
func (d *Decoder) DecodeMessage() (Message, error) {
	payload, err := d.Decode()
	if err != nil {
		return nil, err
	}
	if len(payload) == 0 {
		return nil, errors.New("empty payload")
	}
	return d.decodeProtobuf(payload)
}

// decodeProtobuf unmarshals a FromRadio message and dispatches to the
// appropriate internal type based on the payload variant.
func (d *Decoder) decodeProtobuf(payload []byte) (Message, error) {
	var fr meshpb.FromRadio
	if err := proto.Unmarshal(payload, &fr); err != nil {
		return nil, fmt.Errorf("unmarshal FromRadio: %w", err)
	}

	switch v := fr.PayloadVariant.(type) {
	case *meshpb.FromRadio_NodeInfo:
		return protoToNodeInfo(v.NodeInfo), nil
	case *meshpb.FromRadio_Packet:
		return d.decodePacket(v.Packet)
	default:
		// Other variants (MyInfo, Config, LogRecord, etc.) are ignored for now.
		return nil, fmt.Errorf("unhandled FromRadio variant: %T", v)
	}
}

// decodePacket handles a MeshPacket payload. If the packet contains decoded
// Data with a known PortNum, it unmarshals the payload into Telemetry or Position.
func (d *Decoder) decodePacket(packet *meshpb.MeshPacket) (Message, error) {
	if packet == nil {
		return nil, errors.New("nil packet")
	}

	decoded, ok := packet.PayloadVariant.(*meshpb.MeshPacket_Decoded)
	if !ok || decoded.Decoded == nil {
		return nil, errors.New("packet has no decoded payload")
	}

	data := decoded.Decoded
	switch data.Portnum {
	case meshpb.PortNum_TELEMETRY_APP:
		return d.decodeTelemetry(data.Payload, packet.From)
	case meshpb.PortNum_POSITION_APP:
		return d.decodePosition(data.Payload, packet.From)
	default:
		return nil, fmt.Errorf("unhandled portnum: %v", data.Portnum)
	}
}

// decodeTelemetry unmarshals a Telemetry protobuf and maps it to our internal type.
func (d *Decoder) decodeTelemetry(payload []byte, from uint32) (*Telemetry, error) {
	var telem meshpb.Telemetry
	if err := proto.Unmarshal(payload, &telem); err != nil {
		return nil, fmt.Errorf("unmarshal Telemetry: %w", err)
	}

	t := &Telemetry{
		NodeID:     nodeIDFromUint(from),
		Timestamp:  protoTimeToTime(telem.Time),
		LastUpdate: time.Now(),
	}

	switch v := telem.Variant.(type) {
	case *meshpb.Telemetry_DeviceMetrics:
		dm := v.DeviceMetrics
		if dm.BatteryLevel != nil {
			t.BatteryLevel = int(*dm.BatteryLevel)
		}
		if dm.Voltage != nil {
			t.Voltage = float64(*dm.Voltage)
		}
		if dm.ChannelUtilization != nil {
			t.ChannelUtilization = float64(*dm.ChannelUtilization)
		}
		if dm.AirUtilTx != nil {
			t.AirUtilTx = float64(*dm.AirUtilTx)
		}
	case *meshpb.Telemetry_EnvironmentMetrics:
		em := v.EnvironmentMetrics
		if em.Temperature != nil {
			t.Temperature = float64(*em.Temperature)
		}
		if em.RelativeHumidity != nil {
			t.RelativeHumidity = float64(*em.RelativeHumidity)
		}
		if em.BarometricPressure != nil {
			t.Pressure = float64(*em.BarometricPressure)
		}
	default:
		// Other telemetry variants ignored for now.
	}

	return t, nil
}

// decodePosition unmarshals a Position protobuf and maps it to our internal type.
func (d *Decoder) decodePosition(payload []byte, from uint32) (*Position, error) {
	var pos meshpb.Position
	if err := proto.Unmarshal(payload, &pos); err != nil {
		return nil, fmt.Errorf("unmarshal Position: %w", err)
	}

	p := &Position{
		NodeID:     nodeIDFromUint(from),
		Timestamp:  protoTimeToTime(pos.Time),
		LastUpdate: time.Now(),
	}
	if pos.LatitudeI != nil {
		p.LatitudeI = *pos.LatitudeI
	}
	if pos.LongitudeI != nil {
		p.LongitudeI = *pos.LongitudeI
	}
	if pos.Altitude != nil {
		p.Altitude = *pos.Altitude
	}

	return p, nil
}

// protoToNodeInfo maps a protobuf NodeInfo to our internal type.
func protoToNodeInfo(ni *meshpb.NodeInfo) *NodeInfo {
	if ni == nil {
		return nil
	}

	n := &NodeInfo{
		NodeID:     nodeIDFromUint(ni.Num),
		LastHeard:  protoTimeToTime(ni.LastHeard),
		LastUpdate: time.Now(),
	}

	if ni.User != nil {
		n.LongName = ni.User.LongName
		n.ShortName = ni.User.ShortName
		if ni.User.HwModel != meshpb.HardwareModel_UNSET {
			n.HwModel = ni.User.HwModel.String()
		}
		if ni.User.Role != meshpb.Config_DeviceConfig_Role(0) {
			n.Role = ni.User.Role.String()
		}
	}

	if ni.Position != nil {
		// NodeInfo may carry an embedded position; we return it as a Position
		// message so the store can handle it. The caller (client.dispatch) will
		// route it appropriately.
		_ = ni.Position // handled separately if needed
	}

	return n
}

// nodeIDFromUint converts a node number to the standard Meshtastic node ID
// format: "!" followed by 8 hex digits.
func nodeIDFromUint(num uint32) string {
	return fmt.Sprintf("!%08x", num)
}

// protoTimeToTime converts a protobuf uint32 seconds-since-epoch to time.Time.
// Zero means unknown/unset.
func protoTimeToTime(t uint32) time.Time {
	if t == 0 {
		return time.Time{}
	}
	return time.Unix(int64(t), 0)
}
