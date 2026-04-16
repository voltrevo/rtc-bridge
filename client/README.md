# rtc-bridge

Browser client for [rtc-bridge](https://github.com/voltrevo/rtc-bridge) — access TCP services directly from the browser over WebRTC, no public IP or open ports required.

Nodes dial out to a coordinator server. The browser discovers nodes via the coordinator's HTTP API, exchanges SDP through it, then communicates directly over a WebRTC data channel.

## Install

```
npm install rtc-bridge
```

## Usage

```ts
import { Coordinator, dial } from 'rtc-bridge';

const coordinator = new Coordinator('https://your-coordinator.example.com');

// List available nodes
const nodes = await coordinator.nodes();

// Dial a node
const node = await dial(coordinator, nodes[0].nodeId);

// Ping
const ms = await node.ping();

// List services exposed by the node
const services = await node.list();

// Verify the node's ed25519 identity
const result = await node.verifyIdentity();
if (result.ok) console.log('verified:', result.pubkey);

// Connect to a named TCP service — returns the raw data channel
const dc = await node.connect('myservice');
dc.addEventListener('message', (e) => console.log(e.data));
dc.send('hello\n');

// Open another service on the same peer connection (no re-signaling)
const dc2 = await node.connect('otherservice');

// Close an individual service channel
dc.close();

// Close the node connection (drops all data channels)
node.close();
```

## API

### `Coordinator`

```ts
new Coordinator(url: string)
coordinator.nodes(): Promise<NodeInfo[]>
coordinator.services(): Promise<Record<string, string[]>>
```

### `dial`

```ts
dial(
  coordinator: Coordinator,
  nodeId: string,
  config?: RTCConfiguration,
): Promise<Node>
```

Negotiates a WebRTC connection via the coordinator. Attempts a fast path with a 1 s ICE gathering timeout, then retries with full gathering if the connection fails.

### `Node`

```ts
node.pc   // RTCPeerConnection

node.ping(): Promise<number>              // round-trip ms
node.list(): Promise<string[]>           // service names
node.verifyIdentity(): Promise<VerifyResult>
node.connect(service: string): Promise<RTCDataChannel>  // open service, returns DC
node.close(): void                        // close the peer connection
```

## Data channel protocol

Each data channel starts in command mode. Commands:

| Command | Response |
|---|---|
| `ping` | `pong` |
| `list` | JSON array of service names |
| `challenge <commitment_hex>` | `challenge-response <r_node_hex>` |
| `verify <r_client_hex>` | `proof <pubkey_hex> <sig_hex>` |
| `connect <service>` | `ok` (then raw TCP bytes) |
