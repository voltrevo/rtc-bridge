# rtc-bridge

Browser client for [rtc-bridge](https://github.com/voltrevo/rtc-bridge) — connect to nodes over WebRTC without requiring public IPs or open ports on the node.

Nodes dial out to a coordinator server. The browser discovers nodes via the coordinator's HTTP API, exchanges SDP through it, then communicates directly over a WebRTC data channel.

## Install

```
npm install rtc-bridge
```

## Usage

```ts
import { Coordinator, connect } from 'rtc-bridge';

const coordinator = new Coordinator('https://your-coordinator.example.com');

// List available nodes
const nodes = await coordinator.nodes();

// Connect to a node (command mode)
const ch = await connect(coordinator, nodes[0].nodeId, nodes[0].services[0]);

// Ping
const ms = await ch.ping();

// List services exposed by the node
const services = await ch.list();

// Verify the node's ed25519 identity
const result = await ch.verifyIdentity();
if (result.ok) console.log('verified:', result.pubkey);

// Bridge the data channel to a named TCP service
await ch.bridge('myservice');
// ch.dc is now a raw data channel piped to the TCP connection

// Open an additional service on the same peer connection (no re-signaling)
const sib = await ch.openSibling('otherservice');
await sib.bridge('otherservice');

// Close just this data channel (leaves the peer connection open)
ch.close();

// Close the whole peer connection (drops all data channels)
ch.pc.close();
```

## API

### `Coordinator`

```ts
new Coordinator(url: string)
coordinator.nodes(): Promise<NodeInfo[]>
coordinator.services(): Promise<Record<string, string[]>>
```

### `connect`

```ts
connect(
  coordinator: Coordinator,
  nodeId: string,
  service: string,
  config?: RTCConfiguration,
): Promise<NodeChannel>
```

Negotiates a WebRTC connection via the coordinator. Attempts a fast path with a 1 s ICE gathering timeout, then retries with full gathering if the connection fails.

### `NodeChannel`

```ts
ch.dc   // RTCDataChannel
ch.pc   // RTCPeerConnection

ch.ping(): Promise<number>              // round-trip ms
ch.list(): Promise<string[]>           // service names
ch.verifyIdentity(): Promise<VerifyResult>
ch.bridge(service?: string): Promise<void>  // switch DC to TCP bridge mode
ch.openSibling(service: string): Promise<NodeChannel>  // new DC on same PC
ch.close(): void                       // close this DC only
```

## Node data channel protocol

Before bridging, the data channel runs a text command loop:

| Command | Response |
|---|---|
| `ping` | `pong` |
| `list` | JSON array of service names |
| `challenge <commitment_hex>` | `challenge-response <r_node_hex>` |
| `verify <r_client_hex>` | `proof <pubkey_hex> <sig_hex>` |
| `<service-name>` | `ok` (then raw TCP bytes) |
