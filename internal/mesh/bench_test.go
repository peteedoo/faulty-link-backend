package mesh

import (
	"testing"
	"time"
)

func BenchmarkStorePutNode(b *testing.B) {
	store := NewStore(5 * time.Minute)
	node := &NodeInfo{
		NodeID:    "!12345678",
		ShortName: "TEST",
		LongName:  "Test Node",
		LastUpdate: time.Now(),
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.PutNode(node)
	}
}

func BenchmarkStoreGetNode(b *testing.B) {
	store := NewStore(5 * time.Minute)
	node := &NodeInfo{
		NodeID:    "!12345678",
		ShortName: "TEST",
		LongName:  "Test Node",
		LastUpdate: time.Now(),
	}
	store.PutNode(node)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.GetNode("!12345678")
	}
}

func BenchmarkStorePutTelemetry(b *testing.B) {
	store := NewStore(5 * time.Minute)
	store.PutNode(&NodeInfo{NodeID: "!12345678", ShortName: "TEST", LastUpdate: time.Now()})
	tele := &Telemetry{
		NodeID:     "!12345678",
		LastUpdate: time.Now(),
		AirUtilTx:  0.5,
		ChannelUtilization: 10.0,
		BatteryLevel: 85,
		Voltage:      4.1,
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.PutTelemetry(tele)
	}
}

func BenchmarkStoreAllNodes(b *testing.B) {
	store := NewStore(5 * time.Minute)
	for i := 0; i < 100; i++ {
		store.PutNode(&NodeInfo{
			NodeID:     "!node" + string(rune(i)),
			ShortName:  "N" + string(rune(i)),
			LastUpdate: time.Now(),
		})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.AllNodes()
	}
}

func BenchmarkTelemetryRingAppend(b *testing.B) {
	ring := NewTelemetryRing(64)
	t := Telemetry{NodeID: "!12345678", LastUpdate: time.Now()}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ring.Append(t)
	}
}

func BenchmarkTelemetryRingAll(b *testing.B) {
	ring := NewTelemetryRing(64)
	for i := 0; i < 64; i++ {
		ring.Append(Telemetry{NodeID: "!12345678", LastUpdate: time.Now()})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ring.All()
	}
}
