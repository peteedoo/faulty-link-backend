package mesh

import (
	"bytes"
	"testing"

	"google.golang.org/protobuf/proto"

	meshpb "github.com/peteedoo/faulty-link-backend/third_party/protobufs/meshtastic"
)

// makeFramed wraps a payload in the real Meshtastic stream frame:
// [0x94][0xc3][len_hi][len_lo][payload].
func makeFramed(payload []byte) []byte {
	var buf bytes.Buffer
	buf.WriteByte(0x94)
	buf.WriteByte(0xc3)
	buf.WriteByte(byte(len(payload) >> 8))
	buf.WriteByte(byte(len(payload)))
	buf.Write(payload)
	return buf.Bytes()
}

func TestDecoderDecode(t *testing.T) {
	payload := []byte("hello meshtastic")
	data := makeFramed(payload)

	d := NewDecoder(bytes.NewReader(data), 1024)
	got, err := d.Decode()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("expected %q, got %q", payload, got)
	}
}

func TestDecoderDecodeEmptyPayload(t *testing.T) {
	data := makeFramed([]byte{})
	d := NewDecoder(bytes.NewReader(data), 1024)
	got, err := d.Decode()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty payload, got %q", got)
	}
}

func TestDecoderDecodeMultiple(t *testing.T) {
	p1 := []byte("first")
	p2 := []byte("second")
	var buf bytes.Buffer
	buf.Write(makeFramed(p1))
	buf.Write(makeFramed(p2))

	d := NewDecoder(&buf, 1024)
	for _, want := range [][]byte{p1, p2} {
		got, err := d.Decode()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("expected %q, got %q", want, got)
		}
	}
}

func TestDecoderOversizedMessage(t *testing.T) {
	payload := make([]byte, 128)
	data := makeFramed(payload)
	d := NewDecoder(bytes.NewReader(data), 64)
	_, err := d.Decode()
	if err != ErrOversizedMessage {
		t.Fatalf("expected ErrOversizedMessage, got %v", err)
	}
}

func TestDecoderDecodeMessageNodeInfo(t *testing.T) {
	fr := &meshpb.FromRadio{
		PayloadVariant: &meshpb.FromRadio_NodeInfo{
			NodeInfo: &meshpb.NodeInfo{
				Num: 0x1234abcd,
				User: &meshpb.User{
					Id:        "!1234abcd",
					LongName:  "Test Node",
					ShortName: "TN",
				},
			},
		},
	}
	payload, err := proto.Marshal(fr)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	data := makeFramed(payload)
	d := NewDecoder(bytes.NewReader(data), 1024)
	msg, err := d.DecodeMessage()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ni, ok := msg.(*NodeInfo)
	if !ok {
		t.Fatalf("expected *NodeInfo, got %T", msg)
	}
	if ni.NodeID != "!1234abcd" {
		t.Errorf("expected NodeID '!1234abcd', got %q", ni.NodeID)
	}
	if ni.LongName != "Test Node" {
		t.Errorf("expected LongName 'Test Node', got %q", ni.LongName)
	}
}

func TestDecoderDecodeMessageTelemetry(t *testing.T) {
	bat := uint32(87)
	volt := float32(4.12)
	telemProto := &meshpb.Telemetry{
		Variant: &meshpb.Telemetry_DeviceMetrics{
			DeviceMetrics: &meshpb.DeviceMetrics{
				BatteryLevel: &bat,
				Voltage:      &volt,
			},
		},
	}
	payload, err := proto.Marshal(telemProto)
	if err != nil {
		t.Fatalf("marshal telemetry failed: %v", err)
	}

	fr := &meshpb.FromRadio{
		PayloadVariant: &meshpb.FromRadio_Packet{
			Packet: &meshpb.MeshPacket{
				From: 0xdeadbeef,
				PayloadVariant: &meshpb.MeshPacket_Decoded{
					Decoded: &meshpb.Data{
						Portnum: meshpb.PortNum_TELEMETRY_APP,
						Payload: payload,
					},
				},
			},
		},
	}
	frPayload, err := proto.Marshal(fr)
	if err != nil {
		t.Fatalf("marshal FromRadio failed: %v", err)
	}

	data := makeFramed(frPayload)
	d := NewDecoder(bytes.NewReader(data), 1024)
	msg, err := d.DecodeMessage()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	telem, ok := msg.(*Telemetry)
	if !ok {
		t.Fatalf("expected *Telemetry, got %T", msg)
	}
	if telem.NodeID != "!deadbeef" {
		t.Errorf("expected NodeID '!deadbeef', got %q", telem.NodeID)
	}
	if telem.BatteryLevel != 87 {
		t.Errorf("expected BatteryLevel 87, got %d", telem.BatteryLevel)
	}
}

func TestDecoderDecodeMessagePosition(t *testing.T) {
	lat := int32(407123456)
	lon := int32(-740123456)
	alt := int32(42)
	posProto := &meshpb.Position{
		LatitudeI:  &lat,
		LongitudeI: &lon,
		Altitude:   &alt,
	}
	payload, err := proto.Marshal(posProto)
	if err != nil {
		t.Fatalf("marshal position failed: %v", err)
	}

	fr := &meshpb.FromRadio{
		PayloadVariant: &meshpb.FromRadio_Packet{
			Packet: &meshpb.MeshPacket{
				From: 0xcafebabe,
				PayloadVariant: &meshpb.MeshPacket_Decoded{
					Decoded: &meshpb.Data{
						Portnum: meshpb.PortNum_POSITION_APP,
						Payload: payload,
					},
				},
			},
		},
	}
	frPayload, err := proto.Marshal(fr)
	if err != nil {
		t.Fatalf("marshal FromRadio failed: %v", err)
	}

	data := makeFramed(frPayload)
	d := NewDecoder(bytes.NewReader(data), 1024)
	msg, err := d.DecodeMessage()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pos, ok := msg.(*Position)
	if !ok {
		t.Fatalf("expected *Position, got %T", msg)
	}
	if pos.NodeID != "!cafebabe" {
		t.Errorf("expected NodeID '!cafebabe', got %q", pos.NodeID)
	}
	if pos.LatitudeI != 407123456 {
		t.Errorf("expected LatitudeI 407123456, got %d", pos.LatitudeI)
	}
}

func TestDecoderDecodeMessageUnknownVariant(t *testing.T) {
	fr := &meshpb.FromRadio{
		PayloadVariant: &meshpb.FromRadio_MyInfo{
			MyInfo: &meshpb.MyNodeInfo{
				MyNodeNum: 0x1234,
			},
		},
	}
	payload, err := proto.Marshal(fr)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	data := makeFramed(payload)
	d := NewDecoder(bytes.NewReader(data), 1024)
	_, err = d.DecodeMessage()
	if err == nil {
		t.Fatal("expected error for unhandled variant")
	}
}
