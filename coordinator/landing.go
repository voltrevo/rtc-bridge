package main

import (
	"embed"
	"encoding/json"
	"math"
	"net/http"
	"sync"
	"time"
)

//go:embed static/demo.html static/rtc-bridge.js static/rtc-bridge.js.map
var staticFiles embed.FS

// sdpTau is the EWMA time constant for SDP exchange rate (6 hours in seconds).
const sdpTau = 6 * 3600.0

// sdpTracker tracks SDP exchange rate using a continuous-time EWMA.
// Each exchange adds 1 to the decaying accumulator; rate = sum * 3600 / τ.
type sdpTracker struct {
	mu   sync.Mutex
	sum  float64
	last time.Time
}

func (s *sdpTracker) record() {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.last.IsZero() {
		dt := now.Sub(s.last).Seconds()
		s.sum *= math.Exp(-dt / sdpTau)
	}
	s.sum++
	s.last = now
}

func (s *sdpTracker) perHour() float64 {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last.IsZero() {
		return 0
	}
	dt := now.Sub(s.last).Seconds()
	return s.sum * math.Exp(-dt/sdpTau) * 3600 / sdpTau
}

// handleStats serves GET /api/stats as JSON.
func (c *coordinator) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	c.mu.RLock()
	nodeCount := len(c.nodes)
	c.mu.RUnlock()

	type response struct {
		ConnectedNodes int     `json:"connectedNodes"`
		SDPPerHour     float64 `json:"sdpPerHour"`
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response{
		ConnectedNodes: nodeCount,
		SDPPerHour:     c.sdp.perHour(),
	})
}

// serveStatic returns a handler that serves an embedded static file.
func serveStatic(path, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := staticFiles.ReadFile(path)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Write(data)
	}
}

// handleLanding serves the HTML landing page.
func (c *coordinator) handleLanding(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(landingHTML))
}

const landingHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>rtc-bridge coordinator</title>
  <style>
    *, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }

    body {
      background: radial-gradient(ellipse at 50% 0%, #111827 0%, #0d1117 65%);
      color: #e6edf3;
      font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', system-ui, sans-serif;
      min-height: 100vh;
      display: flex;
      flex-direction: column;
      align-items: center;
      justify-content: center;
      padding: 2rem;
      gap: 0;
    }

    .wordmark {
      font-size: 3rem;
      font-weight: 800;
      letter-spacing: -0.06em;
      background: linear-gradient(135deg, #58a6ff 0%, #a371f7 100%);
      -webkit-background-clip: text;
      -webkit-text-fill-color: transparent;
      background-clip: text;
      margin-bottom: 0.4rem;
      user-select: none;
    }

    .tagline {
      font-size: 1rem;
      color: #8b949e;
      margin-bottom: 3rem;
      text-align: center;
      letter-spacing: 0.01em;
    }

    .stats {
      display: flex;
      gap: 1.5rem;
      margin-bottom: 2.5rem;
      flex-wrap: wrap;
      justify-content: center;
    }

    .card {
      background: #161b22;
      border: 1px solid #30363d;
      border-radius: 14px;
      padding: 1.75rem 2.75rem;
      text-align: center;
      min-width: 190px;
      transition: border-color 0.3s;
    }

    .card:hover { border-color: #58a6ff44; }

    .card-value {
      font-size: 3rem;
      font-weight: 700;
      color: #58a6ff;
      line-height: 1;
      margin-bottom: 0.6rem;
      font-variant-numeric: tabular-nums;
      letter-spacing: -0.03em;
      transition: color 0.4s;
    }

    .card-label {
      font-size: 0.78rem;
      color: #8b949e;
      text-transform: uppercase;
      letter-spacing: 0.08em;
      line-height: 1.4;
    }

    .card-sub {
      font-size: 0.72rem;
      color: #484f58;
      margin-top: 0.2rem;
    }

    .divider {
      width: 100%;
      max-width: 520px;
      border: none;
      border-top: 1px solid #21262d;
      margin-bottom: 2rem;
    }

    .description {
      max-width: 520px;
      color: #8b949e;
      font-size: 0.9rem;
      line-height: 1.7;
    }

    .description a { color: inherit; text-decoration: none; }
    .description a:hover code { border-color: #58a6ff; color: #58a6ff; }

    code {
      background: #161b22;
      border: 1px solid #30363d;
      border-radius: 4px;
      padding: 0.1em 0.4em;
      font-size: 0.85em;
      color: #e6edf3;
      font-family: 'SF Mono', 'Fira Code', Consolas, monospace;
    }

    .api-table {
      margin-top: 2rem;
      width: 100%;
      max-width: 520px;
      border-collapse: collapse;
      font-size: 0.83rem;
    }

    .api-table th {
      color: #6e7681;
      text-transform: uppercase;
      letter-spacing: 0.06em;
      font-size: 0.72rem;
      font-weight: 600;
      text-align: left;
      padding: 0.4rem 0.75rem;
      border-bottom: 1px solid #21262d;
    }

    .api-table td {
      padding: 0.5rem 0.75rem;
      border-bottom: 1px solid #21262d;
      color: #8b949e;
      vertical-align: top;
    }

    .api-table td:first-child { color: #e6edf3; }
    .api-table tr:last-child td { border-bottom: none; }

    .method {
      display: inline-block;
      font-size: 0.7rem;
      font-family: 'SF Mono', 'Fira Code', Consolas, monospace;
      padding: 0.1em 0.4em;
      border-radius: 3px;
      margin-right: 0.3em;
      font-weight: 600;
    }

    .get  { background: #0d419d22; color: #58a6ff; border: 1px solid #1f6feb44; }
    .post { background: #1a7f3722; color: #3fb950; border: 1px solid #238636aa; }
    .ws   { background: #6e40c922; color: #a371f7; border: 1px solid #8957e544; }

    .api-table a {
      color: inherit;
      text-decoration: none;
    }
    .api-table a:hover code { border-color: #58a6ff; color: #58a6ff; }

    .cta {
      display: inline-block;
      margin-top: 1rem;
      margin-bottom: 1rem;
      padding: 0.6rem 1.4rem;
      background: #1f6feb;
      border: 1px solid #388bfd;
      border-radius: 8px;
      color: #fff;
      font-size: 0.9rem;
      font-weight: 600;
      text-decoration: none;
      transition: background 0.15s;
    }
    .cta:hover { background: #388bfd; }
  </style>
</head>
<body>
  <div class="wordmark">rtc-bridge</div>
  <div class="tagline">WebRTC data channel → TCP service bridge &amp; coordinator</div>
  <a class="cta" href="/demo.html">Try it →</a>

  <div class="stats">
    <div class="card">
      <div class="card-value" id="nodes">—</div>
      <div class="card-label">Connected Nodes</div>
    </div>
    <div class="card">
      <div class="card-value" id="sdp">—</div>
      <div class="card-label">SDP Exchanges / hr</div>
      <div class="card-sub">6h EWMA</div>
    </div>
  </div>

  <hr class="divider">

  <div class="description">
    Nodes dial in via <code>WebSocket</code> and authenticate with an ed25519 keypair,
    then register their TCP services. Browsers discover nodes via <a href="/nodes"><code>/nodes</code></a>
    or <a href="/services"><code>/services</code></a>, exchange SDP through <code>/offer</code>, and connect
    directly over WebRTC — no public IP or open ports required on nodes.
  </div>

  <table class="api-table">
    <thead>
      <tr>
        <th>Endpoint</th>
        <th>Description</th>
      </tr>
    </thead>
    <tbody>
      <tr>
        <td><span class="method ws">WS</span><code>/ws</code></td>
        <td>Node registration &amp; signaling</td>
      </tr>
      <tr>
        <td><span class="method get">GET</span><a href="/nodes"><code>/nodes</code></a></td>
        <td>List nodes with public keys &amp; services</td>
      </tr>
      <tr>
        <td><span class="method get">GET</span><a href="/services"><code>/services</code></a></td>
        <td>Map of service name → node IDs</td>
      </tr>
      <tr>
        <td><span class="method post">POST</span><code>/offer</code></td>
        <td>Forward SDP offer, return answer</td>
      </tr>
    </tbody>
  </table>

  <script>
    (async () => {
      try {
        const r = await fetch('/api/stats');
        if (!r.ok) throw new Error('HTTP ' + r.status);
        const d = await r.json();
        const rate = d.sdpPerHour;
        document.getElementById('nodes').textContent = d.connectedNodes;
        document.getElementById('sdp').textContent =
          rate < 10 ? rate.toFixed(1) : Math.round(rate).toString();
      } catch(e) {
        document.getElementById('nodes').textContent = '?';
        document.getElementById('sdp').textContent = '?';
      }
    })();
  </script>
</body>
</html>
`
