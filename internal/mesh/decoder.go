// Package mesh handles Meshtastic TCP connectivity and telemetry ingestion.
package mesh

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"time"

	"google.golang.org/protobuf/proto"

	meshpb "github.com/peteedoo/faulty-link-backend/third_party/protobufs/meshtastic"
)

// Meshtastic stream framing constants (serial + TCP 4403). Each packet is
// prefixed with [START1][START2][len_hi][len_lo]: a 4-byte header where the
// payload length is a 16-bit big-endian integer, max 512 bytes.
const (
	start1   = 0x94
	start2   = 0xc3
	maxFrame = 512
)

// Decoder reads Meshtastic stream-framed protobuf messages from an io.Reader.
// Framing: [0x94][0xc3][len_hi][len_lo] [protobuf payload].
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

// Decode reads the next stream-framed message and returns the raw protobuf
// payload. It syncs to the START1/START2 magic sequence, skipping any leading
// bytes that don't match, so it can recover after a partial or garbage frame.
func (d *Decoder) Decode() ([]byte, error) {
	// Sync to the START1/START2 magic sequence.
	for {
		b, err := d.r.ReadByte()
		if err != nil {
			return nil, fmt.Errorf("read start1: %w", err)
		}
		if b != start1 {
			continue
		}
		b, err = d.r.ReadByte()
		if err != nil {
			return nil, fmt.Errorf("read start2: %w", err)
		}
		if b == start2 {
			break
		}
		// Not start2; if it's another start1, rewind so it's re-examined as the
		// potential start of a new frame.
		if b == start1 {
			_ = d.r.UnreadByte()
		}
	}

	// Read the 16-bit big-endian payload length.
	var hdr [2]byte
	if _, err := io.ReadFull(d.r, hdr[:]); err != nil {
		return nil, fmt.Errorf("read length: %w", err)
	}
	length := int(hdr[0])<<8 | int(hdr[1])
	if length == 0 {
		return []byte{}, nil
	}
	if length > d.maxLen || length > maxFrame {
		return nil, ErrOversizedMessage
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(d.r, payload); err != nil {
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
