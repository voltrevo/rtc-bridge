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

// Dial a node — establishes the WebRTC peer connection
const node = await dial(coordinator, nodes[0].nodeId);

// Each call opens a new data channel in command mode
const ch = await node.createChannel();

// Ping
const ms = await ch.ping();

// List services exposed by the node
const services = await ch.list();

// Verify the node's ed25519 identity
const result = await ch.verifyIdentity();
if (result.ok) console.log('verified:', result.pubkey);

// Connect to a named TCP service — channel transitions to pipe mode
await ch.connect('myservice');
ch.dc.addEventListener('message', (e) => console.log(e.data));
ch.dc.send('hello\n');

// Open another service on the same peer connection (no re-signaling)
const ch2 = await node.createChannel();
await ch2.connect('otherservice');

// Close an individual channel
ch.close();

// Close the node connection (drops all channels)
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

node.createChannel(): Promise<Channel>  // open a new data channel in command mode
node.close(): void                      // close the peer connection
```

### `Channel`

```ts
ch.dc   // RTCDataChannel (use directly once in pipe mode)

ch.ping(): Promise<number>              // round-trip ms
ch.list(): Promise<string[]>           // service names
ch.verifyIdentity(): Promise<VerifyResult>
ch.connect(service: string): Promise<void>  // transition to pipe mode; command methods throw after this
ch.close(): void                            // close this data channel
```

Calling `ping`, `list`, `verifyIdentity`, or `connect` after `connect()` has been called throws an error — the channel is in pipe mode and `ch.dc` should be used directly for reads and writes.

## Data channel protocol

Each data channel starts in command mode. Commands:

| Command | Response |
|---|---|
| `ping` | `pong` |
| `list` | JSON array of service names |
| `challenge <commitment_hex>` | `challenge-response <r_node_hex>` |
| `verify <r_client_hex>` | `proof <pubkey_hex> <sig_hex>` |
| `connect <service>` | `ok` (then raw TCP bytes) |
