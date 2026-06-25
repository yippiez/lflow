package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"
)

// KernelClient manages a single long-lived Jupyter kernel WebSocket connection.
// One KernelClient == one Python interpreter: variables defined in one Execute
// call persist into the next, for as long as the client stays open.
type KernelClient struct {
	conn           *websocket.Conn
	sessionID      string // client session ID used in WS message headers
	jupyterSession string // Jupyter server session ID (for cleanup on close)
	kernelID       string
	proxyURL       string
	proxyToken     string
	http           *http.Client
}

// jupyterMessage is the Jupyter messaging protocol message format.
type jupyterMessage struct {
	Channel      string                 `json:"channel"`
	Header       jupyterHeader          `json:"header"`
	ParentHeader map[string]interface{} `json:"parent_header"`
	Metadata     map[string]interface{} `json:"metadata"`
	Content      map[string]interface{} `json:"content"`
}

type jupyterHeader struct {
	MsgID    string `json:"msg_id"`
	MsgType  string `json:"msg_type"`
	Session  string `json:"session"`
	Username string `json:"username"`
	Version  string `json:"version"`
	Date     string `json:"date"`
}

// WARNING: streamBuffer batches rapid stream messages to reduce WebSocket frame
// volume. Tools like tqdm can emit hundreds of redraws per second; flushing
// every 50ms collapses them into a handful of writes. Removing or changing this
// buffer can cause intermittent EOF disconnects from the Colab proxy when
// running tools that redraw progress bars.
type streamBuffer struct {
	mu      sync.Mutex
	buffers map[string]*strings.Builder
	timer   *time.Timer
	flushFn func(stream, text string)
}

func newStreamBuffer(flushFn func(stream, text string)) *streamBuffer {
	return &streamBuffer{
		buffers: make(map[string]*strings.Builder),
		flushFn: flushFn,
	}
}

func (sb *streamBuffer) write(stream, text string) {
	sb.mu.Lock()
	b, ok := sb.buffers[stream]
	if !ok {
		b = &strings.Builder{}
		sb.buffers[stream] = b
	}
	b.WriteString(text)
	// WARNING: 50ms is the empirically-tested minimum that prevents disconnects.
	// Do not increase the flush interval without re-testing against tqdm-heavy
	// workloads (e.g. model weight downloads).
	if sb.timer == nil {
		sb.timer = time.AfterFunc(50*time.Millisecond, sb.flush)
	}
	sb.mu.Unlock()
}

func (sb *streamBuffer) flush() {
	sb.mu.Lock()
	for stream, b := range sb.buffers {
		text := b.String()
		b.Reset()
		if text != "" {
			sb.flushFn(stream, text)
		}
	}
	sb.timer = nil
	sb.mu.Unlock()
}

func (sb *streamBuffer) flushAndStop() {
	sb.mu.Lock()
	if sb.timer != nil {
		sb.timer.Stop()
		sb.timer = nil
	}
	for stream, b := range sb.buffers {
		text := b.String()
		b.Reset()
		if text != "" {
			sb.flushFn(stream, text)
		}
	}
	sb.mu.Unlock()
}

// newKernelClient creates a Jupyter session and connects to the kernel over a
// WebSocket. The connection (and its keepalive ping loop) live until Close, or
// until ctx is canceled.
func newKernelClient(ctx context.Context, rt *Runtime) (*KernelClient, error) {
	httpClient := &http.Client{Timeout: 60 * time.Second}
	endpoint, proxyToken := rt.ConnectionInfo()
	if endpoint == "" {
		return nil, fmt.Errorf("no proxy URL available — runtime may not be fully assigned")
	}
	validatedEndpoint, err := validateRuntimeProxyURL(endpoint)
	if err != nil {
		logRuntimeProxyValidationFailure(endpoint, err)
		return nil, fmt.Errorf("invalid runtime proxy URL: %w", err)
	}
	endpoint = validatedEndpoint

	var sessResp struct {
		ID     string `json:"id"`
		Kernel struct {
			ID string `json:"id"`
		} `json:"kernel"`
	}

	// Retry session creation — the Jupyter server can take a while to start on a
	// fresh runtime.
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * 5 * time.Second):
			}
		}

		sessURL := endpoint + "/api/sessions"
		sessBody := strings.NewReader(`{"kernel":{"name":"python3"},"name":"lflow","path":"lflow.ipynb","type":"notebook"}`)

		req, err := http.NewRequestWithContext(ctx, "POST", sessURL, sessBody)
		if err != nil {
			return nil, fmt.Errorf("create session request: %w", err)
		}
		req.Header.Set("X-Colab-Runtime-Proxy-Token", proxyToken)
		req.Header.Set("X-Colab-Client-Agent", clientAgent)
		req.Header.Set("Content-Type", "application/json")

		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("create session: %w", err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
			lastErr = fmt.Errorf("create session failed (status %d): %s", resp.StatusCode, body)
			continue
		}

		if err := json.Unmarshal(body, &sessResp); err != nil {
			return nil, fmt.Errorf("parse session response: %w (body: %s)", err, body)
		}

		if sessResp.Kernel.ID == "" {
			lastErr = fmt.Errorf("no kernel ID in session response: %s", body)
			continue
		}

		break
	}

	if sessResp.Kernel.ID == "" {
		return nil, fmt.Errorf("kernel session creation failed after retries: %w", lastErr)
	}

	clientSession := uuid.New().String()

	wsURL := strings.Replace(endpoint, "https://", "wss://", 1)
	wsURL = strings.Replace(wsURL, "http://", "ws://", 1)
	wsURL += "/api/kernels/" + sessResp.Kernel.ID + "/channels?session_id=" + clientSession

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"X-Colab-Runtime-Proxy-Token": {proxyToken},
			"X-Colab-Client-Agent":        {clientAgent},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("websocket connect: %w", err)
	}

	// 10MB read limit — Jupyter can return large outputs (e.g., training logs).
	conn.SetReadLimit(10 * 1024 * 1024)

	kc := &KernelClient{
		conn:           conn,
		sessionID:      clientSession,
		kernelID:       sessResp.Kernel.ID,
		proxyURL:       endpoint,
		proxyToken:     proxyToken,
		http:           httpClient,
		jupyterSession: sessResp.ID,
	}

	if err := kc.waitReady(ctx); err != nil {
		conn.Close(websocket.StatusNormalClosure, "")
		return nil, fmt.Errorf("kernel not ready: %w", err)
	}

	go kc.pingLoop(ctx)

	return kc, nil
}

// pingLoop sends periodic pings to keep the WebSocket alive.
func (kc *KernelClient) pingLoop(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := kc.conn.Ping(ctx); err != nil {
				return
			}
		}
	}
}

// waitReady sends a kernel_info_request and waits for the reply.
func (kc *KernelClient) waitReady(ctx context.Context) error {
	msgID := uuid.New().String()

	msg := jupyterMessage{
		Channel: "shell",
		Header: jupyterHeader{
			MsgID:    msgID,
			MsgType:  "kernel_info_request",
			Session:  kc.sessionID,
			Username: "lflow",
			Version:  "5.3",
			Date:     time.Now().UTC().Format(time.RFC3339),
		},
		ParentHeader: map[string]interface{}{},
		Metadata:     map[string]interface{}{},
		Content:      map[string]interface{}{},
	}

	if err := wsjson.Write(ctx, kc.conn, msg); err != nil {
		return fmt.Errorf("send kernel_info_request: %w", err)
	}

	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	for {
		var reply jupyterMessage
		if err := wsjson.Read(readCtx, kc.conn, &reply); err != nil {
			return fmt.Errorf("wait for kernel ready: %w", err)
		}
		if reply.Header.MsgType == "kernel_info_reply" {
			return nil
		}
	}
}

// Execute runs Python code on the kernel and returns the combined output.
func (kc *KernelClient) Execute(ctx context.Context, code string) (string, error) {
	return kc.ExecuteStream(ctx, code, nil)
}

// ExecuteStream runs Python code and streams output via onOutput. If onOutput is
// nil, output is collected and returned as a string.
func (kc *KernelClient) ExecuteStream(ctx context.Context, code string, onOutput func(stream, text string)) (string, error) {
	msgID := uuid.New().String()

	msg := jupyterMessage{
		Channel: "shell",
		Header: jupyterHeader{
			MsgID:    msgID,
			MsgType:  "execute_request",
			Session:  kc.sessionID,
			Username: "lflow",
			Version:  "5.3",
			Date:     time.Now().UTC().Format(time.RFC3339),
		},
		ParentHeader: map[string]interface{}{},
		Metadata:     map[string]interface{}{},
		Content: map[string]interface{}{
			"code":             code,
			"silent":           false,
			"store_history":    true,
			"allow_stdin":      false,
			"stop_on_error":    true,
			"user_expressions": map[string]interface{}{},
		},
	}

	if err := wsjson.Write(ctx, kc.conn, msg); err != nil {
		return "", fmt.Errorf("send execute_request: %w", err)
	}

	var output strings.Builder
	var gotExecuteReply bool
	var gotIdle bool
	var execStatus string
	var execErrName, execErrValue string

	var buf *streamBuffer
	if onOutput != nil {
		buf = newStreamBuffer(onOutput)
	}

	for {
		var reply jupyterMessage
		if err := wsjson.Read(ctx, kc.conn, &reply); err != nil {
			if buf != nil {
				buf.flushAndStop()
			}
			return output.String(), fmt.Errorf("read message: %w", err)
		}

		// Only process messages for our request.
		parentMsgID, _ := reply.ParentHeader["msg_id"].(string)
		if parentMsgID != msgID {
			continue
		}

		switch reply.Header.MsgType {
		case "stream":
			text, _ := reply.Content["text"].(string)
			name, _ := reply.Content["name"].(string)
			if buf != nil {
				buf.write(name, text)
			}
			output.WriteString(text)

		case "execute_result":
			data, _ := reply.Content["data"].(map[string]interface{})
			if text, ok := data["text/plain"].(string); ok {
				if buf != nil {
					buf.write("stdout", text+"\n")
				}
				output.WriteString(text)
				output.WriteString("\n")
			}

		case "error":
			ename, _ := reply.Content["ename"].(string)
			evalue, _ := reply.Content["evalue"].(string)
			traceback, _ := reply.Content["traceback"].([]interface{})

			errMsg := fmt.Sprintf("%s: %s", ename, evalue)
			if buf != nil {
				buf.write("stderr", errMsg+"\n")
				for _, tb := range traceback {
					if s, ok := tb.(string); ok {
						buf.write("stderr", s+"\n")
					}
				}
			}
			output.WriteString(errMsg)
			output.WriteString("\n")
			for _, tb := range traceback {
				if s, ok := tb.(string); ok {
					output.WriteString(s)
					output.WriteString("\n")
				}
			}

		case "status":
			state, _ := reply.Content["execution_state"].(string)
			if state == "idle" {
				gotIdle = true
				if gotExecuteReply {
					if buf != nil {
						buf.flushAndStop()
					}
					if execStatus == "error" {
						return "", fmt.Errorf("execution error: %s: %s", execErrName, execErrValue)
					}
					return output.String(), nil
				}
			}

		case "execute_reply":
			execStatus, _ = reply.Content["status"].(string)
			if execStatus == "error" {
				execErrName, _ = reply.Content["ename"].(string)
				execErrValue, _ = reply.Content["evalue"].(string)
			}
			gotExecuteReply = true
			if gotIdle {
				if buf != nil {
					buf.flushAndStop()
				}
				if execStatus == "error" {
					return "", fmt.Errorf("execution error: %s: %s", execErrName, execErrValue)
				}
				return output.String(), nil
			}
		}
	}
}

// Close closes the WebSocket connection and deletes the Jupyter session.
func (kc *KernelClient) Close() error {
	delURL := kc.proxyURL + "/api/sessions/" + kc.jupyterSession
	req, err := http.NewRequest("DELETE", delURL, nil)
	if err == nil {
		req.Header.Set("X-Colab-Runtime-Proxy-Token", kc.proxyToken)
		req.Header.Set("X-Colab-Client-Agent", clientAgent)
		resp, err := kc.http.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}

	return kc.conn.Close(websocket.StatusNormalClosure, "done")
}
