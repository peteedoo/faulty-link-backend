// Package mesh handles Meshtastic TCP connectivity and telemetry ingestion.
package mesh

import "time"

// NodeInfo represents a discovered mesh node.
type NodeInfo struct {
	NodeID     string    `json:"node_id"`
	LongName   string    `json:"long_name"`
	ShortName  string    `json:"short_name"`
	HwModel    string    `json:"hw_model"`
	Role       string    `json:"role"`
	LastHeard  time.Time `json:"last_heard"`
	LastUpdate time.Time `json:"-"`
}

// Telemetry represents device and environment metrics from a mesh node.
type Telemetry struct {
	NodeID             string    `json:"node_id"`
	Timestamp          time.Time `json:"timestamp"`
	BatteryLevel       int       `json:"battery_level"`
	Voltage            float64   `json:"voltage"`
	Temperature        float64   `json:"temperature"`
	AirUtilTx          float64   `json:"air_util_tx"`
	ChannelUtilization float64   `json:"channel_utilization"`
	Pressure           float64   `json:"pressure"`
	RelativeHumidity   float64   `json:"relative_humidity"`
	LastUpdate         time.Time `json:"-"`
}

// Position represents a geographic position update from a mesh node.
type Position struct {
	NodeID        string    `json:"node_id"`
	Timestamp     time.Time `json:"timestamp"`
	LatitudeI     int32     `json:"latitude_i"`
	LongitudeI    int32     `json:"longitude_i"`
	Altitude      int32     `json:"altitude"`
	PrecisionBits uint32    `json:"precision_bits"`
	LastUpdate    time.Time `json:"-"`
}

// Message is a discriminated union for decoded mesh messages.
type Message interface {
	messageTag()
}

func (n *NodeInfo) messageTag()  {}
func (t *Telemetry) messageTag() {}
func (p *Position) messageTag()  {}
