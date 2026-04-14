# webrtc-forward

Bridge a browser WebRTC data channel to a local TCP service.

## Quick start

```bash
# 1. Start the echo stub (or any TCP service)
./echo/echo 127.0.0.1:7777

# 2. Start the forwarder
./webrtc-forward 127.0.0.1:7777

# 3. Open demo.html in a browser
#    - Click "Create Offer"
#    - Copy the offer JSON and paste it into the terminal
#    - Copy the answer JSON the tool prints and paste it into the browser
#    - Click "Set Answer"
#    - The chat box appears — type messages and see them echoed back uppercased
```

## How it works

```
Browser (data channel) <--WebRTC/SCTP--> webrtc-forward <--TCP--> host:port
```

Signaling is manual copy-paste (SDP offer/answer). ICE candidates are gathered
before the offer/answer is printed, so the JSON is self-contained (no trickle
ICE required).

## Building

```bash
GONOSUMDB="*" GOPROXY="direct" go build -o webrtc-forward .
go build -o echo/echo ./echo/
```
