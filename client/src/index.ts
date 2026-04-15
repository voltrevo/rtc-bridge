// rtc-mesh browser client library

// ── Utilities ─────────────────────────────────────────────────────────────────

function toHex(bytes: Uint8Array): string {
  return Array.from(bytes, b => b.toString(16).padStart(2, '0')).join('');
}

function fromHex(hex: string): Uint8Array {
  if (hex.length % 2 !== 0) throw new Error('invalid hex length');
  const out = new Uint8Array(hex.length / 2);
  for (let i = 0; i < out.length; i++) {
    out[i] = parseInt(hex.slice(i * 2, i * 2 + 2), 16);
  }
  return out;
}

function xor32(a: Uint8Array, b: Uint8Array): Uint8Array {
  const out = new Uint8Array(32);
  for (let i = 0; i < 32; i++) out[i] = a[i] ^ b[i];
  return out;
}

async function sha256(data: Uint8Array): Promise<Uint8Array> {
  return new Uint8Array(await crypto.subtle.digest('SHA-256', data.buffer as ArrayBuffer));
}

async function pubkeyToNodeId(pubkeyBytes: Uint8Array): Promise<string> {
  const hash = await sha256(pubkeyBytes);
  let n = 0n;
  for (const b of hash) n = (n << 8n) | BigInt(b);
  return n.toString(36).slice(0, 30);
}

// ── Types ──────────────────────────────────────────────────────────────────────

export interface NodeInfo {
  nodeId: string;
  services: string[];
}

export type VerifyResult =
  | { ok: true;  nodeId: string; pubkey: string }
  | { ok: false; message: string };

// ── Coordinator ────────────────────────────────────────────────────────────────

export class Coordinator {
  private readonly base: string;

  constructor(url: string) {
    this.base = url.replace(/\/$/, '');
  }

  async nodes(): Promise<NodeInfo[]> {
    const r = await fetch(`${this.base}/nodes`);
    if (!r.ok) throw new Error(`/nodes: HTTP ${r.status}`);
    return r.json() as Promise<NodeInfo[]>;
  }

  async services(): Promise<Record<string, string[]>> {
    const r = await fetch(`${this.base}/services`);
    if (!r.ok) throw new Error(`/services: HTTP ${r.status}`);
    return r.json() as Promise<Record<string, string[]>>;
  }

  /** @internal */
  async _sendOffer(
    nodeId: string,
    service: string,
    offer: RTCSessionDescriptionInit,
  ): Promise<RTCSessionDescriptionInit> {
    const r = await fetch(`${this.base}/offer`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ nodeId, service, offer }),
    });
    if (!r.ok) throw new Error(`/offer: HTTP ${r.status} — ${await r.text()}`);
    return r.json() as Promise<RTCSessionDescriptionInit>;
  }
}

// ── NodeChannel ────────────────────────────────────────────────────────────────

const DEFAULT_CONFIG: RTCConfiguration = {
  iceServers: [{ urls: 'stun:stun.l.google.com:19302' }],
};

export class NodeChannel {
  readonly dc: RTCDataChannel;
  readonly pc: RTCPeerConnection;
  private readonly _nodeId: string;
  private readonly _service: string;

  constructor(dc: RTCDataChannel, pc: RTCPeerConnection, nodeId: string, service: string) {
    this.dc = dc;
    this.pc = pc;
    this._nodeId = nodeId;
    this._service = service;
  }

  private sendCmd(cmd: string, timeoutMs = 10_000): Promise<string> {
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.dc.removeEventListener('message', handler);
        reject(new Error(`timeout waiting for response to: ${JSON.stringify(cmd)}`));
      }, timeoutMs);
      const handler = (e: MessageEvent) => {
        clearTimeout(timer);
        this.dc.removeEventListener('message', handler);
        const text = typeof e.data === 'string'
          ? e.data
          : new TextDecoder().decode(e.data as ArrayBuffer);
        resolve(text);
      };
      this.dc.addEventListener('message', handler);
      this.dc.send(cmd);
    });
  }

  async ping(): Promise<number> {
    const t0 = performance.now();
    const resp = await this.sendCmd('ping');
    if (resp !== 'pong') throw new Error(`unexpected ping response: ${resp}`);
    return Math.round(performance.now() - t0);
  }

  async list(): Promise<string[]> {
    const resp = await this.sendCmd('list');
    return JSON.parse(resp) as string[];
  }

  async verifyIdentity(): Promise<VerifyResult> {
    if (!crypto.subtle) {
      return { ok: false, message: 'crypto.subtle unavailable (requires HTTPS or localhost)' };
    }

    const rClient = crypto.getRandomValues(new Uint8Array(32));
    const commitment = await sha256(rClient);

    const resp1 = await this.sendCmd('challenge ' + toHex(commitment));
    if (!resp1.startsWith('challenge-response ')) {
      return { ok: false, message: `unexpected challenge response: ${resp1}` };
    }
    let rNode: Uint8Array;
    try {
      rNode = fromHex(resp1.slice('challenge-response '.length).trim());
    } catch {
      return { ok: false, message: 'invalid hex in challenge-response' };
    }
    if (rNode.length !== 32) {
      return { ok: false, message: `r_node wrong length: ${rNode.length}` };
    }

    const resp2 = await this.sendCmd('verify ' + toHex(rClient));
    if (!resp2.startsWith('proof ')) {
      return { ok: false, message: `unexpected verify response: ${resp2}` };
    }
    const parts = resp2.trim().split(' ');
    if (parts.length !== 3) {
      return { ok: false, message: `malformed proof (${parts.length} parts)` };
    }
    const [, pubkeyHex, sigHex] = parts;

    let pubkeyBytes: Uint8Array, sigBytes: Uint8Array;
    try {
      pubkeyBytes = fromHex(pubkeyHex);
      sigBytes    = fromHex(sigHex);
    } catch {
      return { ok: false, message: 'invalid hex in proof' };
    }

    const joint  = xor32(rClient, rNode);
    const prefix = new TextEncoder().encode('rtc-mesh:dc-challenge:');
    const msg    = new Uint8Array(prefix.length + joint.length);
    msg.set(prefix);
    msg.set(joint, prefix.length);

    let sigValid: boolean;
    try {
      const key = await crypto.subtle.importKey(
        'raw', pubkeyBytes.buffer as ArrayBuffer, { name: 'Ed25519' }, false, ['verify'],
      );
      sigValid = await crypto.subtle.verify(
        'Ed25519', key, sigBytes.buffer as ArrayBuffer, msg.buffer as ArrayBuffer,
      );
    } catch (e) {
      return { ok: false, message: `crypto error: ${e}` };
    }

    if (!sigValid) {
      return { ok: false, message: 'signature invalid' };
    }

    const derivedId = await pubkeyToNodeId(pubkeyBytes);
    if (derivedId !== this._nodeId) {
      return { ok: false, message: `nodeId mismatch (derived ${derivedId}, expected ${this._nodeId})` };
    }

    return { ok: true, nodeId: derivedId, pubkey: pubkeyHex };
  }

  /**
   * Send the service name over the command channel, switching it to TCP bridge mode.
   * Omit service to use the one passed to connect().
   */
  async bridge(service?: string): Promise<void> {
    const svc = service ?? this._service;
    const resp = await this.sendCmd(svc);
    if (resp !== 'ok') throw new Error(`bridge failed: ${resp}`);
  }

  close(): void {
    this.dc.close();
    this.pc.close();
  }
}

// ── connect ────────────────────────────────────────────────────────────────────

/**
 * Negotiate a WebRTC connection to a node via the coordinator and return
 * an open NodeChannel in command mode.
 */
export async function connect(
  coordinator: Coordinator,
  nodeId: string,
  service: string,
  config?: RTCConfiguration,
): Promise<NodeChannel> {
  const pc = new RTCPeerConnection(config ?? DEFAULT_CONFIG);
  const dc = pc.createDataChannel('proxy');

  const offer = await pc.createOffer();
  await pc.setLocalDescription(offer);

  // Wait for ICE gathering to complete before sending the offer.
  await new Promise<void>((resolve) => {
    if (pc.iceGatheringState === 'complete') { resolve(); return; }
    pc.addEventListener('icegatheringstatechange', function check() {
      if (pc.iceGatheringState === 'complete') {
        pc.removeEventListener('icegatheringstatechange', check);
        resolve();
      }
    });
  });

  const answer = await coordinator._sendOffer(nodeId, service, pc.localDescription!);
  await pc.setRemoteDescription(answer);

  // Wait for the data channel to open.
  await new Promise<void>((resolve, reject) => {
    if (dc.readyState === 'open') { resolve(); return; }
    dc.addEventListener('open', () => resolve(), { once: true });
    pc.addEventListener('connectionstatechange', () => {
      if (pc.connectionState === 'failed' || pc.connectionState === 'closed') {
        reject(new Error(`peer connection ${pc.connectionState}`));
      }
    });
  });

  return new NodeChannel(dc, pc, nodeId, service);
}
