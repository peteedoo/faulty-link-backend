# Build Instructions

## Protobuf Generation

Meshtastic protobuf definitions are vendored in `third_party/protobufs/`.

To regenerate Go code after updating `.proto` files:

```bash
cd third_party/protobufs

export PATH="$PATH:$(go env GOPATH)/bin"

protoc --go_out=. --go_opt=paths=source_relative \
  meshtastic/mesh.proto \
  meshtastic/telemetry.proto \
  meshtastic/portnums.proto \
  meshtastic/config.proto \
  meshtastic/channel.proto \
  meshtastic/module_config.proto \
  meshtastic/device_ui.proto \
  meshtastic/xmodem.proto \
  meshtastic/remote_hardware.proto \
  meshtastic/admin.proto \
  meshtastic/deviceonly.proto \
  meshtastic/clientonly.proto \
  meshtastic/connection_status.proto \
  meshtastic/apponly.proto \
  meshtastic/atak.proto \
  meshtastic/cannedmessages.proto \
  meshtastic/interdevice.proto \
  meshtastic/localonly.proto \
  meshtastic/mqtt.proto \
  meshtastic/paxcount.proto \
  meshtastic/powermon.proto \
  meshtastic/rtttl.proto \
  meshtastic/serial_hal.proto \
  meshtastic/storeforward.proto \
  nanopb.proto
```

> **Note:** `deviceonly.pb.go` references `file_nanopb_proto_init()` which is not generated. After running protoc, remove that line from `meshtastic/deviceonly.pb.go`.

## Build & Test

```bash
go build ./cmd/bridge
go test ./...
go vet ./...
```

## Run

```bash
go run ./cmd/bridge
```

The bridge listens on `:8080` and attempts TCP connection to the Meshtastic node on port 4403.
