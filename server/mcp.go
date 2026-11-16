package main

// MCP is deliberately a small, stateless JSON-RPC transport. It shares the
// console's operations and state, never a second copy of the deployment engine.
import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const mcpVersion = "2025-11-25"
const mcpMaxMessage = 1 << 20

type mcpControl struct {
	SettingsPath   string
	MediaDir       string
	ImportDir      string
	Bound          net.Addr
	PXEDir         string
	PXE            *PXE
	jobsMu         sync.Mutex
	jobs           map[string]*mediaJob
	mediaTransport http.RoundTripper // optional test transport; nil uses verified system TLS
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func rpcFail(id json.RawMessage, code int, message string) rpcResponse {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{code, message}}
}

func supportedMCP(v string) bool {
	return v == "2025-03-26" || v == "2025-06-18" || v == mcpVersion
}

func (s *Server) mcpHTTP(w http.ResponseWriter, r *http.Request) {
	if s.MCP == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	// Unlike the preview console, management always needs a token, even locally.
	p, authenticated := s.bearerPrincipal(r)
	if !authenticated || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || p.Role == "" {
		http.Error(w, "MCP requires a valid Authorization: Bearer credential (server or personal account token)", http.StatusUnauthorized)
		return
	}
	if crossSiteBrowser(r) {
		http.Error(w, "MCP origin refused", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return // no unsolicited SSE stream or sessions
	}
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	if v := r.Header.Get("MCP-Protocol-Version"); v != "" && !supportedMCP(v) {
		http.Error(w, "unsupported MCP-Protocol-Version", http.StatusBadRequest)
		return
	}
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, mcpMaxMessage))
	if err != nil {
		http.Error(w, "MCP message exceeds 1 MiB", http.StatusRequestEntityTooLarge)
		return
	}
	response := s.mcpDispatch(context.WithValue(r.Context(), principalKey{}, p), b)
	if response == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeJSON(w, response)
}

func (s *Server) mcpDispatch(ctx context.Context, b []byte) *rpcResponse {
	respond := func(v rpcResponse) *rpcResponse { return &v }
	if !json.Valid(b) {
		return respond(rpcFail(nil, -32700, "Parse error"))
	}
	var q rpcRequest
	if err := json.Unmarshal(b, &q); err != nil || q.JSONRPC != "2.0" || q.Method == "" {
		return respond(rpcFail(nil, -32600, "Invalid Request; send one JSON-RPC 2.0 object"))
	}
	if len(q.ID) > 0 {
		var id any
		_ = json.Unmarshal(q.ID, &id)
		switch id.(type) {
		case string, float64:
		default:
			return respond(rpcFail(nil, -32600, "id must be a string or number"))
		}
	} else {
		return nil
	} // notifications never receive a response
	if len(q.Params) > 0 && (q.Params[0] != '{' || !json.Valid(q.Params)) {
		return respond(rpcFail(q.ID, -32602, "params must be an object"))
	}
	var result any
	switch q.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string         `json:"protocolVersion"`
			Capabilities    map[string]any `json:"capabilities"`
			ClientInfo      map[string]any `json:"clientInfo"`
		}
		if json.Unmarshal(q.Params, &p) != nil || p.ProtocolVersion == "" || p.Capabilities == nil || p.ClientInfo == nil {
			return respond(rpcFail(q.ID, -32602, "initialize needs protocolVersion, capabilities and clientInfo"))
		}
		v := p.ProtocolVersion
		if !supportedMCP(v) {
			v = mcpVersion
		}
		result = map[string]any{"protocolVersion": v, "capabilities": map[string]any{"tools": map[string]any{}, "resources": map[string]any{}, "prompts": map[string]any{}}, "serverInfo": map[string]any{"name": "minute2win", "version": "0.6.0"}, "instructions": mcpInstructions}
	case "ping":
		result = map[string]any{}
	case "tools/list":
		result = map[string]any{"tools": mcpTools()}
	case "tools/call":
		var rawParams map[string]json.RawMessage
		_ = json.Unmarshal(q.Params, &rawParams)
		if raw, exists := rawParams["arguments"]; exists {
			raw = bytes.TrimSpace(raw)
			if len(raw) == 0 || raw[0] != '{' {
				return respond(rpcFail(q.ID, -32602, "arguments must be an object"))
			}
		}
		var p struct {
			Name      string                     `json:"name"`
			Arguments map[string]json.RawMessage `json:"arguments"`
		}
		if json.Unmarshal(q.Params, &p) != nil || p.Name == "" {
			return respond(rpcFail(q.ID, -32602, "tools/call needs name and object arguments"))
		}
		spec, ok := mcpToolNamed(p.Name)
		if !ok {
			return respond(rpcFail(q.ID, -32602, "Unknown tool: "+p.Name))
		}
		if err := validateToolArgs(spec, p.Arguments); err != nil {
			return respond(rpcFail(q.ID, -32602, err.Error()))
		}
		result = s.callMCPTool(ctx, p.Name, p.Arguments)
	case "resources/list":
		result = map[string]any{"resources": mcpResources()}
	case "resources/templates/list":
		result = map[string]any{"resourceTemplates": []any{}}
	case "resources/read":
		var p struct {
			URI string `json:"uri"`
		}
		_ = json.Unmarshal(q.Params, &p)
		body, ok := mcpResource(p.URI)
		if !ok {
			return respond(rpcFail(q.ID, -32602, "Unknown resource; call resources/list"))
		}
		result = map[string]any{"contents": []any{map[string]any{"uri": p.URI, "mimeType": "text/markdown", "text": body}}}
	case "prompts/list":
		result = map[string]any{"prompts": []any{map[string]any{"name": "guided_deployment", "description": "Guide setup, media acquisition, preflight and one deployment without losing track of destructive intent."}}}
	case "prompts/get":
		var p struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(q.Params, &p)
		if p.Name != "guided_deployment" {
			return respond(rpcFail(q.ID, -32602, "Unknown prompt"))
		}
		result = map[string]any{"description": "Guided minute2win deployment", "messages": []any{map[string]any{"role": "user", "content": map[string]any{"type": "text", "text": mcpInstructions + "\nCall setup_status, then setup_guide for the selected installer. Work through blockers, acquire verified media, save a profile, run profile_validate with probe=true, then arm the exact MAC the user authorized. Use deployment_get to monitor and diagnose. Do not treat silence as success."}}}}
	default:
		return respond(rpcFail(q.ID, -32601, "Method not found"))
	}
	return respond(rpcResponse{JSONRPC: "2.0", ID: q.ID, Result: result})
}

// The stdio mode is a bridge to the ONE running server. It must never open a
// state file or bind DHCP ports itself. stdout is reserved for protocol messages.
func runMCPBridge(ctx context.Context, in io.Reader, out io.Writer, endpoint, token string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("-mcp-url must be an http(s) MCP endpoint without credentials, query or fragment")
	}
	if u.Scheme == "http" && !listenIsLoopback(net.JoinHostPort(u.Hostname(), "1")) {
		return fmt.Errorf("use HTTPS for a remote -mcp-url; HTTP is allowed only on loopback")
	}
	if token == "" {
		return fmt.Errorf("M2W_TOKEN is required for -mcp-stdio")
	}
	client := &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	scan := bufio.NewScanner(in)
	scan.Buffer(make([]byte, 4096), mcpMaxMessage)
	for scan.Scan() {
		b := bytes.TrimSpace(scan.Bytes())
		if len(b) == 0 {
			continue
		}
		var q rpcRequest
		_ = json.Unmarshal(b, &q)
		req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(b))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := client.Do(req)
		var response any
		if err != nil {
			response = rpcFail(q.ID, -32000, "Cannot reach minute2win; start the server with -mcp and verify -mcp-url")
		} else {
			body, readErr := io.ReadAll(io.LimitReader(res.Body, 4*mcpMaxMessage+1))
			res.Body.Close()
			if res.StatusCode == http.StatusAccepted {
				continue
			}
			if readErr != nil || len(body) > 4*mcpMaxMessage || res.StatusCode != 200 || !json.Valid(body) {
				response = rpcFail(q.ID, -32000, fmt.Sprintf("minute2win MCP HTTP %d; check -mcp, M2W_TOKEN, endpoint and proxy", res.StatusCode))
			} else {
				response = json.RawMessage(body)
			}
		}
		if len(q.ID) == 0 && json.Valid(b) {
			continue
		}
		if err := json.NewEncoder(out).Encode(response); err != nil {
			return err
		}
	}
	return scan.Err()
}

func mcpStdioMain(endpoint string) {
	if err := runMCPBridge(context.Background(), os.Stdin, os.Stdout, endpoint, os.Getenv("M2W_TOKEN")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
