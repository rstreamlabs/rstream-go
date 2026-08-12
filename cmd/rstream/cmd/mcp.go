// See LICENSE file in the project root for license information.

package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/webtty"
	"github.com/spf13/cobra"
)

type mcpMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpDecodeError struct {
	Code int
}

func (e *mcpDecodeError) Error() string {
	if e.Code == -32700 {
		return "invalid MCP JSON"
	}
	return "invalid MCP JSON-RPC request"
}

type mcpToolCallParams struct {
	Name      string                     `json:"name"`
	Arguments map[string]json.RawMessage `json:"arguments,omitempty"`
}

type mcpEnvelope struct {
	Message mcpMessage
	Framing mcpFraming
}

type mcpReadResult struct {
	Envelope mcpEnvelope
	Err      error
}

type mcpMessageHandler func(context.Context, mcpMessage) mcpResponse

type mcpRequestJob struct {
	Context context.Context
	Cancel  context.CancelFunc
	Key     string
	Message mcpMessage
	Framing mcpFraming
}

type mcpRequestState struct {
	Cancel    context.CancelFunc
	Method    string
	Cancelled bool
}

type mcpRequestRegistry struct {
	mu       sync.Mutex
	requests map[string]*mcpRequestState
}

type mcpResponseWriter struct {
	ctx       context.Context
	output    io.Writer
	jobs      chan mcpWriteJob
	done      chan struct{}
	errors    chan<- error
	cancelMCP context.CancelFunc
}

type mcpWriteJob struct {
	Response mcpResponse
	Framing  mcpFraming
	Result   chan error
}

type mcpFraming string

const mcpProtocolVersion = "2025-11-25"
const mcpFramingContentLength mcpFraming = "content-length"
const mcpFramingLineDelimited mcpFraming = "line-delimited"
const mcpMaxMessageBytes = 8 * 1024 * 1024
const mcpMaxConcurrentRequests = 8
const mcpMaxQueuedRequests = 16
const mcpInstructions = "Write the product name as rstream. When local runtime state is unknown, call rstream_runtime_status first. Treat the returned agent_guidance as authoritative for application SDK selection and self-hosted CE boundaries. If login is missing during a setup or prepare flow, call rstream_auth_start, show the login_url without restating a user code unless the tool returned a separate user_code field, then call rstream_auth_poll with wait=true and a bounded timeout; only ask the user to report approval after the wait times out. If the user only asks to start login and return an approval URL, stop after rstream_auth_start. If a login token exists but no usable hosted project context exists, or the requested hosted project differs from the selected context, call rstream_runtime_prepare with the project name, endpoint, or ID. If the user is using a self-hosted rstream Engine Community Edition deployment, do not use hosted project, workspace, billing, plan, rstream Auth, managed credential, managed policy, hosted logs, managed TURN, or Control plane tools as if they existed; CE agents use a direct engine host, a locally signed JWT, static TLS certificates, and an engine-only context or RSTREAM_ENGINE/RSTREAM_AUTHENTICATION_TOKEN. The public CE runtime scope is the TCP/TLS engine listener, optional HTTP redirect listener, static TLS certificate provider, JWT agent authentication, Prometheus metrics, bytestream tunnels, published HTTP/TLS tunnels over the TCP/TLS listener, and private bytestream tunnels. Do not describe QUIC, DTLS, datagram tunnels, WebTTY, browser rstream Auth, HTTP tunnel token auth, challenge mode, zero-trust hosted edge policy, managed resource policies, hosted project settings, hosted credential records, automatic certificates, Geo/IP policies, trusted IP policies, or managed logs as CE features. Tunnel cleanup must follow the resource owner: unmanaged rstream forward processes are cleaned up by stopping the owning process, while MCP-created resources use their returned cleanup fields and matching MCP stop tools. If no tunnel project is available during a hosted setup or prepare flow, report that a project is needed; do not create a project unless the user explicitly asks to create one. If a tool reports missing authorization for a user-approved MCP action, start a new rstream login with the additional required permission; explicit permissions are added to the MCP workstation bundle instead of replacing it. If several projects are available and the user did not explicitly name one, do not infer or recommend a project from naming conventions alone; list the choices and ask the user to choose by name, endpoint, or ID without adding a preferred example. For Codex workstation local tunnels, WebTTY, remote exposure, and remote MCP on hosted projects, do not call rstream_token_create; it is only for immediate browser, URL, query-token, published MCP, or runtime handoff flows. Enabling token_auth on a tunnel only configures edge access control; do not mint a token unless the user asks to hand one to a client or to verify authenticated access. For remote MCP surfaces that Codex calls itself, keep the remote exposure private unless the user asks for a public URL or browser access. Scoped credentials for remote devices are not the same thing as short-lived delegated handoff tokens. For application-owned tunnel lifecycle, prefer SDKs over MCP or shell workflows and name the concrete SDK in the answer: Node.js tunnel runtimes use @rstreamlabs/runtime with Client, createTunnel, serve, dial/private dialing, AbortSignal-based cancellation, and tunnel close/cleanup; Node.js hosted/Engine API clients use @rstreamlabs/tunnels for inventory, watch, token, and TURN workflows; Go services use github.com/rstreamlabs/rstream-go with Connect, CreateTunnel, Dial, context cancellation, and Close; native C++ services use the rstream C++ SDK from github.com/rstreamlabs/rstream-cpp with io_rstrm::client, async_create_tunnel, async_accept, io_rstrm::socket, and io_rstrm::endpoint. The rstream CLI is an operator and sidecar surface, not the preferred primary API inside application code when an SDK covers the runtime. In SDK answers, describe MCP as setup, diagnostics, managed local tunnels, and remote operations. Do not shell out to package registries to discover these SDK names during normal MCP workflows. For Engine inventory endpoints /api/clients and /api/tunnels, use Authorization: Bearer tokens. For Engine watch endpoints /api/sse and /api/websocket, Authorization: Bearer is accepted and the rstream.token query parameter is also accepted for browser transports that cannot attach headers; that query-token form is only for those watch endpoints and must use a short-lived auth or app token with explicit read-only watch permissions and list-only tunnel resources, not personal, create, connect, WebTTY session, or WebTTY log permissions. CE Engine HTTP APIs use the CE JWT authentication backend and do not enforce hosted resources.tunnels boundaries. Use rstream_local_tunnel_expose, rstream_local_tunnel_list, and rstream_local_tunnel_stop for MCP-managed local tunnel workflows. When a local tunnel or remote exposure is created by MCP, use the returned structured cleanup fields and the matching MCP stop tool for cleanup; do not invent shell commands for MCP-managed resources. Do not shell out to the rstream CLI for information already exposed by this MCP server, and do not use a local shell for presentation-only transformations of MCP results; use the MCP context, runtime, project, local tunnel, WebTTY, and remote tools instead."

var mcpCmd = &cobra.Command{
	GroupID:      "utils",
	Use:          "mcp",
	Short:        "Run rstream MCP integrations",
	SilenceUsage: true,
	RunE:         func(cmd *cobra.Command, args []string) error { return cmd.Help() },
}

var mcpServeCmd = &cobra.Command{
	Use:          "serve",
	Short:        "Serve rstream tools over MCP stdio",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		restoreEnv := applyMCPServeFlagEnvironment(cmd)
		defer restoreEnv()
		return serveMCP(cmd.Context(), os.Stdin, os.Stdout)
	},
}

func init() {
	mcpCmd.Flags().SortFlags = false
	mcpCmd.PersistentFlags().SortFlags = false
	mcpServeCmd.Flags().SortFlags = false
	mcpCmd.AddCommand(mcpServeCmd)
	rootCmd.AddCommand(mcpCmd)
}

func applyMCPServeFlagEnvironment(cmd *cobra.Command) func() {
	previous := map[string]*string{}
	for flag, env := range map[string]string{"api-url": "RSTREAM_API_URL", "config": "RSTREAM_CONFIG", "context": "RSTREAM_CONTEXT"} {
		value := mcpStringFlag(cmd, flag)
		if strings.TrimSpace(value) == "" {
			continue
		}
		if old, ok := os.LookupEnv(env); ok {
			previous[env] = &old
		} else {
			previous[env] = nil
		}
		_ = os.Setenv(env, value)
	}
	return func() {
		for env, old := range previous {
			if old == nil {
				_ = os.Unsetenv(env)
			} else {
				_ = os.Setenv(env, *old)
			}
		}
	}
}

func mcpStringFlag(cmd *cobra.Command, name string) string {
	value, err := cmd.Flags().GetString(name)
	if err == nil && strings.TrimSpace(value) != "" {
		return value
	}
	value, err = cmd.InheritedFlags().GetString(name)
	if err == nil {
		return value
	}
	return ""
}

func serveMCP(ctx context.Context, input io.Reader, output io.Writer) error {
	return serveMCPWithHandler(ctx, input, output, handleMCPMessage)
}

func serveMCPWithHandler(ctx context.Context, input io.Reader, output io.Writer, handler mcpMessageHandler) error {
	serverCtx, cancelServer := context.WithCancel(ctx)
	defer cancelServer()
	reader := bufio.NewReader(input)
	results := make(chan mcpReadResult, 1)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		defer close(results)
		for {
			envelope, err := readMCPEnvelope(reader)
			select {
			case results <- mcpReadResult{Envelope: envelope, Err: err}:
			case <-serverCtx.Done():
				return
			}
			if err != nil {
				var decodeErr *mcpDecodeError
				if errors.As(err, &decodeErr) {
					continue
				}
				return
			}
		}
	}()
	writeErrors := make(chan error, 1)
	writer := newMCPResponseWriter(serverCtx, output, writeErrors, cancelServer)
	registry := &mcpRequestRegistry{requests: map[string]*mcpRequestState{}}
	jobs := make(chan mcpRequestJob, mcpMaxQueuedRequests)
	var workers sync.WaitGroup
	for range mcpMaxConcurrentRequests {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range jobs {
				if !runMCPRequestJob(job, handler, writer, registry, writeErrors, cancelServer) {
					return
				}
			}
		}()
	}
	finish := func(err error, abort bool) error {
		var inputCloser io.Closer
		var outputCloser io.Closer
		abortServer := func() {
			cancelServer()
			registry.cancelAll()
			inputCloser, _ = input.(io.Closer)
			outputCloser, _ = output.(io.Closer)
			if inputCloser != nil {
				_ = inputCloser.Close()
			}
			if outputCloser != nil {
				_ = outputCloser.Close()
			}
		}
		if abort {
			abortServer()
		}
		close(jobs)
		workersDone := make(chan struct{})
		go func() {
			workers.Wait()
			close(workersDone)
		}()
		if abort {
			<-workersDone
		} else {
			select {
			case <-workersDone:
			case <-ctx.Done():
				abort = true
				abortServer()
				<-workersDone
			case writeErr := <-writeErrors:
				abort = true
				err = writeErr
				abortServer()
				<-workersDone
			}
		}
		writer.close()
		if inputCloser != nil {
			select {
			case <-readerDone:
			case <-time.After(time.Second):
				if err == nil {
					err = errors.New("MCP input did not close after cancellation")
				}
			}
		}
		if err != nil {
			return err
		}
		if abort && ctx.Err() != nil {
			return nil
		}
		select {
		case err := <-writeErrors:
			return err
		default:
			return nil
		}
	}
	for {
		select {
		case <-ctx.Done():
			return finish(nil, true)
		case err := <-writeErrors:
			return finish(err, true)
		case result, ok := <-results:
			if !ok {
				return finish(nil, false)
			}
			if errors.Is(result.Err, io.EOF) {
				return finish(nil, false)
			}
			if result.Err != nil {
				var decodeErr *mcpDecodeError
				if errors.As(result.Err, &decodeErr) {
					response := mcpResponse{
						JSONRPC: "2.0",
						ID:      json.RawMessage("null"),
						Error:   &mcpError{Code: decodeErr.Code, Message: mcpErrorMessage(decodeErr.Code)},
					}
					if err := writer.write(serverCtx, response, result.Envelope.Framing); err != nil {
						return finish(err, true)
					}
					continue
				}
				return finish(result.Err, true)
			}
			if result.Envelope.Message.ID == nil {
				if protocolErr := validateMCPMessage(result.Envelope.Message); protocolErr != nil {
					response := mcpResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: protocolErr}
					if err := writer.write(serverCtx, response, result.Envelope.Framing); err != nil {
						return finish(err, true)
					}
					continue
				}
				if result.Envelope.Message.Method == "notifications/cancelled" {
					registry.cancel(result.Envelope.Message.Params)
				}
				continue
			}
			if protocolErr := validateMCPMessage(result.Envelope.Message); protocolErr != nil {
				response := mcpResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: protocolErr}
				if err := writer.write(serverCtx, response, result.Envelope.Framing); err != nil {
					return finish(err, true)
				}
				continue
			}
			key := mcpRequestKey(result.Envelope.Message.ID)
			requestCtx, requestCancel := context.WithCancel(serverCtx)
			if !registry.register(key, result.Envelope.Message.Method, requestCancel) {
				requestCancel()
				response := mcpResponse{JSONRPC: "2.0", ID: result.Envelope.Message.ID, Error: &mcpError{Code: -32600, Message: "request id is already in progress"}}
				if err := writer.write(serverCtx, response, result.Envelope.Framing); err != nil {
					return finish(err, true)
				}
				continue
			}
			job := mcpRequestJob{Context: requestCtx, Cancel: requestCancel, Key: key, Message: result.Envelope.Message, Framing: result.Envelope.Framing}
			select {
			case jobs <- job:
			default:
				registry.complete(key)
				requestCancel()
				response := mcpResponse{JSONRPC: "2.0", ID: result.Envelope.Message.ID, Error: &mcpError{Code: -32000, Message: "server is busy"}}
				if err := writer.write(serverCtx, response, result.Envelope.Framing); err != nil {
					return finish(err, true)
				}
			}
		}
	}
}

func runMCPRequestJob(job mcpRequestJob, handler mcpMessageHandler, writer *mcpResponseWriter, registry *mcpRequestRegistry, writeErrors chan<- error, cancelServer context.CancelFunc) bool {
	defer job.Cancel()
	if job.Context.Err() != nil {
		registry.complete(job.Key)
		return true
	}
	response := handler(job.Context, job.Message)
	if registry.complete(job.Key) || job.Context.Err() != nil {
		return true
	}
	if err := writer.write(job.Context, response, job.Framing); err != nil {
		select {
		case writeErrors <- err:
		default:
		}
		cancelServer()
		return false
	}
	return true
}

func newMCPResponseWriter(ctx context.Context, output io.Writer, errors chan<- error, cancel context.CancelFunc) *mcpResponseWriter {
	writer := &mcpResponseWriter{ctx: ctx, output: output, jobs: make(chan mcpWriteJob, mcpMaxConcurrentRequests+mcpMaxQueuedRequests), done: make(chan struct{}), errors: errors, cancelMCP: cancel}
	go writer.run()
	return writer
}

func (w *mcpResponseWriter) run() {
	defer close(w.done)
	for {
		select {
		case <-w.ctx.Done():
			return
		case job, ok := <-w.jobs:
			if !ok {
				return
			}
			err := writeMCPResponseWithFraming(w.output, job.Response, job.Framing)
			job.Result <- err
			if err != nil {
				select {
				case w.errors <- err:
				default:
				}
				w.cancelMCP()
				return
			}
		}
	}
}

func (w *mcpResponseWriter) write(ctx context.Context, response mcpResponse, framing mcpFraming) error {
	result := make(chan error, 1)
	job := mcpWriteJob{Response: response, Framing: framing, Result: result}
	select {
	case w.jobs <- job:
	case <-ctx.Done():
		return ctx.Err()
	case <-w.done:
		return io.ErrClosedPipe
	}
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		select {
		case err := <-result:
			return err
		default:
			return ctx.Err()
		}
	case <-w.done:
		select {
		case err := <-result:
			return err
		default:
			return io.ErrClosedPipe
		}
	}
}

func (w *mcpResponseWriter) close() {
	close(w.jobs)
	<-w.done
}

func (r *mcpRequestRegistry) register(key string, method string, cancel context.CancelFunc) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.requests[key]; ok {
		return false
	}
	r.requests[key] = &mcpRequestState{Cancel: cancel, Method: method}
	return true
}

func (r *mcpRequestRegistry) complete(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	request, ok := r.requests[key]
	if !ok {
		return false
	}
	delete(r.requests, key)
	return request.Cancelled
}

func (r *mcpRequestRegistry) cancel(params json.RawMessage) {
	key, ok := mcpCancellationRequestKey(params)
	if !ok {
		return
	}
	r.mu.Lock()
	request, ok := r.requests[key]
	if !ok || request.Method == "initialize" {
		r.mu.Unlock()
		return
	}
	request.Cancelled = true
	cancel := request.Cancel
	r.mu.Unlock()
	cancel()
}

func (r *mcpRequestRegistry) cancelAll() {
	r.mu.Lock()
	requests := r.requests
	r.requests = map[string]*mcpRequestState{}
	r.mu.Unlock()
	for _, request := range requests {
		request.Cancel()
	}
}

func mcpCancellationRequestKey(params json.RawMessage) (string, bool) {
	var value struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	if err := json.Unmarshal(params, &value); err != nil || value.RequestID == nil {
		return "", false
	}
	var id any
	if err := json.Unmarshal(value.RequestID, &id); err != nil {
		return "", false
	}
	switch id.(type) {
	case string, float64:
		return mcpRequestKey(value.RequestID), true
	default:
		return "", false
	}
}

func mcpRequestKey(id json.RawMessage) string {
	var compact bytes.Buffer
	if err := json.Compact(&compact, id); err != nil {
		return string(id)
	}
	return compact.String()
}

func readMCPMessage(reader *bufio.Reader) (mcpMessage, error) {
	envelope, err := readMCPEnvelope(reader)
	if err != nil {
		return mcpMessage{}, err
	}
	return envelope.Message, nil
}

func readMCPEnvelope(reader *bufio.Reader) (mcpEnvelope, error) {
	contentLength := -1
	line, err := reader.ReadString('\n')
	if err != nil {
		return mcpEnvelope{}, err
	}
	line = strings.TrimRight(line, "\r\n")
	trimmedLine := strings.TrimSpace(line)
	if strings.HasPrefix(trimmedLine, "{") || !strings.Contains(line, ":") {
		message, err := decodeMCPMessage([]byte(trimmedLine))
		if err != nil {
			return mcpEnvelope{Framing: mcpFramingLineDelimited}, err
		}
		return mcpEnvelope{Message: message, Framing: mcpFramingLineDelimited}, nil
	}
	if parsed, ok, err := parseMCPContentLengthHeader(line); err != nil {
		return mcpEnvelope{}, err
	} else if ok {
		contentLength = parsed
	}
	for {
		line, err = reader.ReadString('\n')
		if err != nil {
			return mcpEnvelope{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if parsed, ok, err := parseMCPContentLengthHeader(line); err != nil {
			return mcpEnvelope{}, err
		} else if ok {
			contentLength = parsed
		}
	}
	if contentLength < 0 {
		return mcpEnvelope{}, errors.New("missing MCP Content-Length")
	}
	if contentLength > mcpMaxMessageBytes {
		return mcpEnvelope{}, fmt.Errorf("MCP Content-Length exceeds %d bytes", mcpMaxMessageBytes)
	}
	payload := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return mcpEnvelope{}, err
	}
	message, err := decodeMCPMessage(payload)
	if err != nil {
		return mcpEnvelope{Framing: mcpFramingContentLength}, err
	}
	return mcpEnvelope{Message: message, Framing: mcpFramingContentLength}, nil
}
func parseMCPContentLengthHeader(line string) (int, bool, error) {
	key, value, ok := strings.Cut(line, ":")
	if !ok || !strings.EqualFold(strings.TrimSpace(key), "Content-Length") {
		return 0, false, nil
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, false, fmt.Errorf("invalid MCP Content-Length: %w", err)
	}
	if parsed < 0 {
		return 0, false, fmt.Errorf("invalid MCP Content-Length: negative value")
	}
	return parsed, true, nil
}
func decodeMCPMessage(payload []byte) (mcpMessage, error) {
	if !json.Valid(payload) {
		return mcpMessage{}, &mcpDecodeError{Code: -32700}
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil || object == nil {
		return mcpMessage{}, &mcpDecodeError{Code: -32600}
	}
	var message mcpMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		return mcpMessage{}, &mcpDecodeError{Code: -32600}
	}
	return message, nil
}
func writeMCPResponse(output io.Writer, response mcpResponse) error {
	return writeMCPResponseWithFraming(output, response, mcpFramingContentLength)
}
func writeMCPResponseWithFraming(output io.Writer, response mcpResponse, framing mcpFraming) error {
	payload, err := json.Marshal(response)
	if err != nil {
		return err
	}
	if framing == mcpFramingLineDelimited {
		if _, err := output.Write(append(payload, '\n')); err != nil {
			return err
		}
		return nil
	}
	if _, err := fmt.Fprintf(output, "Content-Length: %d\r\n\r\n", len(payload)); err != nil {
		return err
	}
	_, err = output.Write(payload)
	return err
}
func handleMCPMessage(ctx context.Context, message mcpMessage) mcpResponse {
	response := mcpResponse{JSONRPC: "2.0", ID: message.ID}
	if protocolErr := validateMCPMessage(message); protocolErr != nil {
		response.ID = json.RawMessage("null")
		response.Error = protocolErr
		return response
	}
	switch message.Method {
	case "initialize":
		if !validMCPInitializeParams(message.Params) {
			response.Error = &mcpError{Code: -32602, Message: mcpErrorMessage(-32602)}
			break
		}
		response.Result = mcpInitializeResult()
	case "tools/list":
		response.Result = map[string]any{"tools": mcpTools()}
	case "tools/call":
		result, err := handleMCPToolCall(ctx, message.Params)
		if err != nil {
			response.Result = mcpToolTextResult(err.Error(), true)
		} else {
			response.Result = result
		}
	default:
		response.Error = &mcpError{Code: -32601, Message: "method not found"}
	}
	return response
}

func validateMCPMessage(message mcpMessage) *mcpError {
	if message.JSONRPC != "2.0" || strings.TrimSpace(message.Method) == "" {
		return &mcpError{Code: -32600, Message: mcpErrorMessage(-32600)}
	}
	if len(message.Params) > 0 {
		var params map[string]json.RawMessage
		if err := json.Unmarshal(message.Params, &params); err != nil || params == nil {
			return &mcpError{Code: -32600, Message: mcpErrorMessage(-32600)}
		}
	}
	if message.ID != nil {
		var id any
		if err := json.Unmarshal(message.ID, &id); err != nil {
			return &mcpError{Code: -32600, Message: mcpErrorMessage(-32600)}
		}
		switch id.(type) {
		case nil, string, float64:
		default:
			return &mcpError{Code: -32600, Message: mcpErrorMessage(-32600)}
		}
	}
	return nil
}

func validMCPInitializeParams(raw json.RawMessage) bool {
	var params struct {
		ProtocolVersion string                     `json:"protocolVersion"`
		Capabilities    map[string]json.RawMessage `json:"capabilities"`
		ClientInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"clientInfo"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return false
	}
	return strings.TrimSpace(params.ProtocolVersion) != "" &&
		params.Capabilities != nil &&
		strings.TrimSpace(params.ClientInfo.Name) != "" &&
		strings.TrimSpace(params.ClientInfo.Version) != ""
}

func mcpErrorMessage(code int) string {
	switch code {
	case -32700:
		return "Parse error"
	case -32602:
		return "Invalid params"
	default:
		return "Invalid Request"
	}
}
func mcpInitializeResult() map[string]any {
	return map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
		"serverInfo":      map[string]any{"name": "rstream", "title": "rstream", "version": rstream.Version},
		"instructions":    mcpInstructions,
	}
}

type mcpToolDefinition struct {
	Name         string
	Title        string
	Description  string
	Properties   map[string]any
	Required     []string
	Annotations  map[string]any
	OutputSchema map[string]any
	Meta         map[string]any
}

type mcpToolBehavior struct {
	Title       string
	ReadOnly    bool
	Destructive bool
	Idempotent  bool
	OpenWorld   bool
}

func mcpTools() []map[string]any {
	return []map[string]any{
		mcpTool("rstream_auth_start", "Start the local rstream OAuth device login flow and return a user approval URL without exposing tokens. Show login_url to the user; do not extract or repeat a code from the URL. A separate user_code field is returned only when the provider cannot embed it in the URL. The default permissions cover the CLI MCP workstation surface; explicit permissions are added to that bundle instead of replacing it.", map[string]any{"api_url": mcpStringSchema("Optional rstream API URL. Defaults to RSTREAM_API_URL or the public rstream API."), "permissions": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional additional permissions to union with the MCP workstation permission bundle."}}, []string{}),
		mcpTool("rstream_auth_poll", "Poll a local rstream OAuth device login session and store the approved token in the CLI config without returning it. After rstream_auth_start in setup or prepare flows, call this with wait=true and a bounded timeout so the user does not need to type a confirmation message.", map[string]any{"id": mcpStringSchema("Auth session ID returned by rstream_auth_start."), "wait": map[string]any{"type": "boolean", "description": "Wait for browser approval instead of polling once."}, "timeout_seconds": map[string]any{"type": "number", "description": "Optional wait timeout in seconds when wait is true."}}, []string{"id"}),
		mcpTool("rstream_context_list", "List local rstream CLI contexts available to this MCP server.", map[string]any{}, []string{}),
		mcpTool("rstream_context_get", "Get one local rstream CLI context, or the default context when name is omitted.", map[string]any{"name": mcpStringSchema("Optional local rstream context name.")}, []string{}),
		mcpTool("rstream_project_creation_options", "Return project creation options and billing actions for a workspace.", map[string]any{"workspace_id": mcpStringSchema("Workspace ID.")}, []string{"workspace_id"}),
		mcpTool("rstream_project_create", "Create a tunnel project, or start Stripe checkout only when start_checkout is true.", map[string]any{"workspace_id": mcpStringSchema("Workspace ID."), "name": mcpStringSchema("Project name."), "routing": mcpStringSchema("Global or regional routing. Defaults to regional."), "provider": mcpStringSchema("Infrastructure provider from project creation options."), "region": mcpStringSchema("Region for regional routing; omit for global routing."), "plan": mcpStringSchema("Project plan from creation options."), "creation_fingerprint": mcpStringSchema("Creation fingerprint from project creation options."), "idempotency_key": mcpStringSchema("Optional idempotency key. A safe key is generated when omitted."), "start_checkout": map[string]any{"type": "boolean", "description": "Start Stripe checkout instead of direct creation. Set only after explicit user approval."}}, []string{"workspace_id", "name", "plan", "creation_fingerprint"}),
		mcpTool("rstream_project_update", "Update a tunnel project's display name.", map[string]any{"project_id": mcpStringSchema("Tunnel project ID to update."), "name": mcpStringSchema("New project name.")}, []string{"project_id", "name"}),
		mcpTool("rstream_project_delete", "Delete a tunnel project after explicit user approval. Use this only for the exact project the user asked to remove.", map[string]any{"project_id": mcpStringSchema("Tunnel project ID to delete.")}, []string{"project_id"}),
		mcpTool("rstream_project_list", "List tunnel projects available through the local rstream Control plane context. If multiple projects are returned and the user did not name one, list the choices and ask for a project name, endpoint, or ID; do not recommend a default from project names such as Dev, Test, or Prod.", map[string]any{"workspace_id": mcpStringSchema("Optional workspace ID. When omitted, lists across accessible workspaces."), "q": mcpStringSchema("Optional project search query."), "page": map[string]any{"type": "number", "description": "Optional result page."}, "page_size": map[string]any{"type": "number", "description": "Optional page size."}, "sort": mcpStringSchema("Optional sort key."), "order": mcpStringSchema("Optional sort direction.")}, []string{}),
		mcpTool("rstream_project_logs", "List tunnel request and connection logs for a tunnel project.", map[string]any{"project_id": mcpStringSchema("Tunnel project ID."), "timeline": mcpStringSchema("Optional timeline: 30m, 1h, 12h, 24h, 3d, 1w, or 30d."), "start": mcpStringSchema("Optional start date."), "end": mcpStringSchema("Optional end date."), "event_type": mcpStringSchema("Optional event type filter."), "after_event_id": mcpStringSchema("Optional event ID cursor."), "page": map[string]any{"type": "number", "description": "Optional result page."}, "page_size": map[string]any{"type": "number", "description": "Optional page size."}, "order": mcpStringSchema("Optional sort direction.")}, []string{"project_id"}),
		mcpTool("rstream_project_events_list", "List canonical lifecycle events for a tunnel project. This is separate from request and connection logs.", map[string]any{"project_id": mcpStringSchema("Tunnel project ID."), "timeline": mcpStringSchema("Optional timeline: 30m, 1h, 12h, 24h, 3d, 1w, or 30d."), "start": mcpStringSchema("Optional start date."), "end": mcpStringSchema("Optional end date."), "event_type": mcpStringSchema("Optional event type filter."), "after_event_id": mcpStringSchema("Optional event ID cursor."), "page": map[string]any{"type": "number", "description": "Optional result page."}, "page_size": map[string]any{"type": "number", "description": "Optional page size."}, "order": mcpStringSchema("Optional sort direction.")}, []string{"project_id"}),
		mcpTool("rstream_project_webhooks_list", "List webhook destinations configured on a tunnel project.", map[string]any{"project_id": mcpStringSchema("Tunnel project ID."), "q": mcpStringSchema("Optional webhook search query."), "status": mcpStringSchema("Optional status filter."), "destination_type": mcpStringSchema("Optional destination type filter."), "page": map[string]any{"type": "number", "description": "Optional result page."}, "page_size": map[string]any{"type": "number", "description": "Optional page size."}, "sort": mcpStringSchema("Optional sort key."), "order": mcpStringSchema("Optional sort direction.")}, []string{"project_id"}),
		mcpTool("rstream_project_webhook_deliveries_list", "List delivery records for a webhook destination, including status and delivery metadata.", map[string]any{"project_id": mcpStringSchema("Tunnel project ID."), "webhook_id": mcpStringSchema("Webhook ID."), "status": mcpStringSchema("Optional status filter."), "event_type": mcpStringSchema("Optional event type filter."), "start": mcpStringSchema("Optional start date."), "end": mcpStringSchema("Optional end date."), "page": map[string]any{"type": "number", "description": "Optional result page."}, "page_size": map[string]any{"type": "number", "description": "Optional page size."}, "order": mcpStringSchema("Optional sort direction.")}, []string{"project_id", "webhook_id"}),
		mcpTool("rstream_project_webhook_delivery_get", "Get one webhook delivery with attempts, request body, response metadata, and errors.", map[string]any{"project_id": mcpStringSchema("Tunnel project ID."), "webhook_id": mcpStringSchema("Webhook ID."), "delivery_id": mcpStringSchema("Delivery ID.")}, []string{"project_id", "webhook_id", "delivery_id"}),
		mcpTool("rstream_project_usage", "Return current-period tunnel and TURN bandwidth usage for a tunnel project.", map[string]any{"project_id": mcpStringSchema("Tunnel project ID.")}, []string{"project_id"}),
		mcpTool("rstream_project_plan_get", "Return a tunnel project's current plan, feature list, and quota metadata before using plan-gated features.", map[string]any{"project_id": mcpStringSchema("Tunnel project ID.")}, []string{"project_id"}),
		mcpTool("rstream_project_turn_usage", "Return TURN relay usage breakdowns for the last 30 days.", map[string]any{"project_id": mcpStringSchema("Tunnel project ID.")}, []string{"project_id"}),
		mcpTool("rstream_project_turn_credentials_create", "Create short-lived TURN credentials for a tunnel project after explicit user approval. The response contains TURN secrets; do not log or paste it unless the user explicitly needs the credential material.", map[string]any{"project_id": mcpStringSchema("Tunnel project ID. Required unless project_endpoint is provided."), "project_endpoint": mcpStringSchema("Tunnel project endpoint. Required unless project_id is provided."), "ttl_seconds": map[string]any{"type": "number", "description": "Optional credential TTL in seconds, between 1 and 3600."}}, []string{}),
		mcpTool("rstream_project_domains_list", "List stable custom domains configured on a tunnel project.", map[string]any{"project_id": mcpStringSchema("Tunnel project ID."), "q": mcpStringSchema("Optional domain search query."), "page": map[string]any{"type": "number", "description": "Optional result page."}, "page_size": map[string]any{"type": "number", "description": "Optional page size."}, "sort": mcpStringSchema("Optional sort key."), "order": mcpStringSchema("Optional sort direction.")}, []string{"project_id"}),
		mcpTool("rstream_project_domain_create", "Attach a custom domain to a tunnel project after explicit user approval.", map[string]any{"project_id": mcpStringSchema("Tunnel project ID."), "hostname": mcpStringSchema("Custom hostname or wildcard base domain to attach."), "kind": map[string]any{"type": "string", "description": "Optional domain kind. Defaults to hostname.", "enum": []string{"hostname", "wildcard"}}, "certificate_validation": map[string]any{"type": "string", "description": "Optional certificate validation method. Defaults to TLS-ALPN-01 for hostnames and DNS-01 for wildcards.", "enum": []string{"tls_alpn_01", "dns_01"}}}, []string{"project_id", "hostname"}),
		mcpTool("rstream_project_domain_get", "Return verification and DNS details for a project domain.", mcpProjectDomainSelectorProperties(), []string{"project_id"}),
		mcpTool("rstream_project_domain_delete", "Remove a custom domain from a tunnel project after explicit user approval.", mcpProjectDomainSelectorProperties(), []string{"project_id"}),
		mcpTool("rstream_project_domain_verify", "Re-check ownership and DNS configuration for a project domain.", mcpProjectDomainSelectorProperties(), []string{"project_id"}),
		mcpTool("rstream_project_domain_connect", "Return a Domain Connect apply URL for a project domain when the DNS provider supports it.", mcpProjectDomainSelectorProperties(), []string{"project_id"}),
		mcpTool("rstream_project_tcp_addresses_list", "List reserved TCP addresses configured on a tunnel project.", map[string]any{"project_id": mcpStringSchema("Tunnel project ID.")}, []string{"project_id"}),
		mcpTool("rstream_project_tcp_address_reserve", "Reserve a TCP address for a tunnel project after explicit user approval.", map[string]any{"project_id": mcpStringSchema("Tunnel project ID.")}, []string{"project_id"}),
		mcpTool("rstream_project_tcp_address_release", "Release a reserved TCP address after explicit user approval.", map[string]any{"project_id": mcpStringSchema("Tunnel project ID."), "port": map[string]any{"type": "number", "description": "Reserved public TCP port."}}, []string{"project_id", "port"}),
		mcpTool("rstream_project_settings_get", "Return access and transport settings for a tunnel project.", map[string]any{"project_id": mcpStringSchema("Tunnel project ID.")}, []string{"project_id"}),
		mcpTool("rstream_project_settings_patch", "Patch access and transport settings for a tunnel project after explicit user approval.", map[string]any{"project_id": mcpStringSchema("Tunnel project ID."), "settings": map[string]any{"type": "object", "description": "Partial project settings object."}}, []string{"project_id", "settings"}),
		mcpTool("rstream_project_settings_reset", "Reset access and transport settings for a tunnel project after explicit user approval.", map[string]any{"project_id": mcpStringSchema("Tunnel project ID.")}, []string{"project_id"}),
		mcpTool("rstream_local_tunnel_expose", mcpLocalTunnelExposeToolDescription(), mcpProjectSelectorProperties(mcpLocalTunnelExposeToolProperties()), []string{"port"}),
		mcpTool("rstream_local_tunnel_list", "List local tunnels started through the local rstream MCP tunnel registry.", map[string]any{}, []string{}),
		mcpTool("rstream_local_tunnel_stop", "Stop a local tunnel from the local rstream MCP tunnel registry.", map[string]any{"id": mcpStringSchema("Local tunnel ID or tunnel ID returned by rstream_local_tunnel_expose.")}, []string{"id"}),
		mcpTool("rstream_remote_expose", "Start rstream forward on a POSIX WebTTY remote host to expose a remote-local network service or MCP surface. For a remote MCP surface that Codex will call itself, set publish=false unless the user asked for a public URL or browser access.", mcpProjectSelectorProperties(map[string]any{"webtty_url": mcpStringSchema("WebTTY URL, for example rstrm://robot-shell."), "exec_path": mcpStringSchema("Advertised exec_path from rstream_webtty_list. Defaults to /."), "known_server": mcpStringSchema("Optional local known WebTTY server name for direct WebTTY URLs."), "port": mcpStringSchema("Remote-local port to expose from the WebTTY host."), "host": mcpStringSchema("Optional remote-local host, defaults to 127.0.0.1."), "id": mcpStringSchema("Optional remote expose ID used for later stop."), "name": mcpStringSchema("Optional rstream tunnel name."), "protocol": mcpStringSchema("Optional protocol: http, h2c, h3, tls, tcp, udp, dtls, or quic."), "publish": map[string]any{"type": "boolean", "description": "Publish the exposed resource. Defaults to true. For Codex-only remote MCP calls, pass false."}, "stable_domain": mcpStringSchema("Optional stable published host."), "tcp_port": map[string]any{"type": "number", "description": "Optional reserved public TCP port. Requires protocol=tcp and publish=true."}, "token_auth": map[string]any{"type": "boolean", "description": "Require rstream token authentication at the edge."}, "rstream_auth": map[string]any{"type": "boolean", "description": "Require rstream account authentication at the edge."}, "mcp_path": mcpStringSchema("Optional remote MCP HTTP path, usually /mcp; adds MCP discovery labels."), "labels": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Additional rstream labels as key=value entries."}, "env": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Environment variables passed to the WebTTY command as KEY=value entries."}, "workdir": mcpStringSchema("Optional remote working directory."), "user": mcpStringSchema("Optional remote username or UID."), "timeout_seconds": map[string]any{"type": "number", "description": "Seconds to wait for the remote tunnel to report online."}, "rstream_command": mcpStringSchema("Optional rstream executable path on the remote host.")}), []string{"webtty_url", "port"}),
		mcpTool("rstream_remote_expose_stop", "Stop a remote expose process previously started through rstream_remote_expose.", mcpProjectSelectorProperties(map[string]any{"webtty_url": mcpStringSchema("WebTTY URL used to reach the remote host."), "exec_path": mcpStringSchema("Advertised exec_path from rstream_webtty_list. Defaults to /."), "known_server": mcpStringSchema("Optional local known WebTTY server name for direct WebTTY URLs."), "id": mcpStringSchema("Remote expose ID returned by rstream_remote_expose."), "env": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Environment variables passed to the WebTTY command as KEY=value entries."}, "workdir": mcpStringSchema("Optional remote working directory."), "user": mcpStringSchema("Optional remote username or UID.")}), []string{"webtty_url", "id"}),
		mcpTool("rstream_remote_mcp_discover", "Discover online MCP surfaces exposed through rstream labels.", mcpProjectSelectorProperties(map[string]any{"filter": mcpStringSchema("Optional additional rstream tunnel filter.")}), []string{}),
		mcpTool("rstream_remote_mcp_tools", "List tools from a remote MCP server reached through rstream or a published URL.", mcpProjectSelectorProperties(map[string]any{"url": mcpStringSchema("Remote MCP URL, such as rstrm://robot-mcp or https://robot.example.com/mcp."), "path": mcpStringSchema("Optional MCP path when url does not include one."), "token": mcpStringSchema("Optional bearer token for token-auth protected published MCP surfaces.")}), []string{"url"}),
		mcpTool("rstream_remote_mcp_call", "Call a tool on a remote MCP server reached through rstream or a published URL.", mcpProjectSelectorProperties(map[string]any{"url": mcpStringSchema("Remote MCP URL, such as rstrm://robot-mcp or https://robot.example.com/mcp."), "tool": mcpStringSchema("Remote MCP tool name."), "path": mcpStringSchema("Optional MCP path when url does not include one."), "token": mcpStringSchema("Optional bearer token for token-auth protected published MCP surfaces."), "arguments": map[string]any{"type": "object", "description": "Remote MCP tool arguments."}, "arguments_json": mcpStringSchema("Remote MCP tool arguments as a JSON object string.")}), []string{"url", "tool"}),
		mcpTool("rstream_runtime_prepare", "Prepare the local rstream runtime for a project on a Codex workstation. This uses the long-lived rstream login credential stored in the CLI config and never mints a short-lived delegated token. Use this before local tunnels, WebTTY, remote exposure, or remote MCP when the selected project or context is missing, stale, or ambiguous. If multiple projects are available and the user did not explicitly name a project, do not guess or recommend one from naming conventions alone; ask the user to choose a project name, endpoint, or ID without adding a preferred example.", mcpProjectSelectorProperties(map[string]any{"context_name": mcpStringSchema("Optional local context name to create or update. Defaults to the selected project name."), "set_default": map[string]any{"type": "boolean", "description": "Set the prepared context as default. Defaults to true."}}), []string{}),
		mcpTool("rstream_runtime_status", "Return local rstream CLI runtime status without exposing secrets. The response includes agent_guidance with authoritative SDK choices, Engine API auth surfaces including /api/sse and /api/websocket query-token rules, and the self-hosted Engine CE feature boundary.", map[string]any{}, []string{}),
		mcpTool("rstream_token_create", "Mint a short-lived delegated rstream auth token for immediate browser, URL, or runtime handoff. Do not use this to configure a Codex workstation, create a rstream context, connect local tunnels or WebTTY, or install long-lived remote devices; use rstream_runtime_prepare for workstation flows and long-lived scoped credentials for remote devices. The response contains a bearer token; summarize its purpose, scope, and lifetime, but do not print the raw token unless the user explicitly asks to see or copy the token value. When scoping Engine access, pass the full token resource object under resources_json. Example for a read-only project token: {\"tunnels\":{\"projects\":[\"PROJECT_ID\"],\"scopes\":{\"tunnels\":{\"list\":true}}}}. Scope actions must match permissions: tunnels.resources.read-only requires list; webtty.sessions.read-only requires list; webtty.logs.read-only requires list; tunnels.tunnels.create-delete requires create; tunnels.streams.create-delete requires connect; webtty.sessions.read-write requires list and connect.", map[string]any{"permissions": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Permissions to include in the minted token."}, "resources_json": mcpStringSchema("Optional full resource boundary JSON object, for example {\"tunnels\":{\"projects\":[\"PROJECT_ID\"],\"scopes\":{\"tunnels\":{\"list\":true}}}} for read-only tunnel or WebTTY access.")}, []string{"permissions"}),
		mcpTool("rstream_workspace_list", "List workspaces available through the local rstream Control plane context.", map[string]any{"type": mcpStringSchema("Optional workspace type filter."), "membershipStatus": mcpStringSchema("Optional membership status filter.")}, []string{}),
		mcpTool("rstream_workspace_members_list", "List users and invited users with access to a workspace and their roles.", map[string]any{"workspace_id": mcpStringSchema("Workspace ID."), "q": mcpStringSchema("Optional member search query."), "page": map[string]any{"type": "number", "description": "Optional result page."}, "page_size": map[string]any{"type": "number", "description": "Optional page size."}, "sort": mcpStringSchema("Optional sort key."), "order": mcpStringSchema("Optional sort direction.")}, []string{"workspace_id"}),
		mcpTool("rstream_webtty_list", "List online WebTTY servers exposed through rstream. Use this before remote command execution, filesystem access, remote exposure, or remote MCP setup; each result advertises exec_path, fs_path, filesystem mode, and capabilities when available.", mcpProjectSelectorProperties(map[string]any{"filter": mcpStringSchema("Optional rstream tunnel filter, such as name=shell or labels.rstream.webtty.label.role=codex. A bare value is treated as a tunnel name."), "name": mcpStringSchema("Optional WebTTY tunnel name to search for.")}), []string{}),
		mcpTool("rstream_webtty_servers_list", "List WebTTY servers visible from the local MCP runtime. Includes registered servers from the Control plane and lightweight online WebTTY tunnels from the Engine.", mcpProjectSelectorProperties(map[string]any{"type": mcpStringSchema("Optional server type: all, lightweight, or registered."), "q": mcpStringSchema("Optional registered server search query."), "status": mcpStringSchema("Optional registered server status."), "filter": mcpStringSchema("Optional lightweight tunnel filter."), "name": mcpStringSchema("Optional lightweight WebTTY name filter."), "page": map[string]any{"type": "number", "description": "Optional registered server page."}, "page_size": map[string]any{"type": "number", "description": "Optional registered server page size."}}), []string{}),
		mcpTool("rstream_webtty_server_get", "Inspect one registered WebTTY server or one lightweight WebTTY tunnel resolved by name, server ID, or tunnel ID.", mcpProjectSelectorProperties(map[string]any{"type": mcpStringSchema("Optional server type: lightweight or registered."), "server_id": mcpStringSchema("Registered WebTTY server ID."), "tunnel_id": mcpStringSchema("Lightweight WebTTY tunnel ID."), "name": mcpStringSchema("WebTTY server or tunnel name.")}), []string{}),
		mcpTool("rstream_webtty_server_create", "Create a registered WebTTY server record. This does not start a remote process; it returns the server record and the local run/enrollment commands.", mcpProjectSelectorProperties(map[string]any{"name": mcpStringSchema("Registered WebTTY server name."), "description": mcpStringSchema("Optional server description."), "recording_policy": mcpStringSchema("Recording policy: recorded or private."), "encryption_policy": mcpStringSchema("Encryption policy: disabled, explicit_key, or workspace_managed."), "access_policy": mcpStringSchema("Access policy: project_members or restricted."), "labels": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional labels as key=value entries."}}), []string{"name"}),
		mcpTool("rstream_webtty_server_update", "Update mutable metadata on a registered WebTTY server record.", mcpProjectSelectorProperties(map[string]any{"server_id": mcpStringSchema("Registered WebTTY server ID."), "name": mcpStringSchema("Optional new server name."), "description": mcpStringSchema("Optional new server description."), "status": mcpStringSchema("Optional status: active or suspended."), "recording_policy": mcpStringSchema("Optional recording policy: recorded or private."), "access_policy": mcpStringSchema("Optional access policy: project_members or restricted."), "labels": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional replacement labels as key=value entries."}}), []string{"server_id"}),
		mcpTool("rstream_webtty_server_delete", "Delete a registered WebTTY server record after explicit user approval.", mcpProjectSelectorProperties(map[string]any{"server_id": mcpStringSchema("Registered WebTTY server ID.")}), []string{"server_id"}),
		mcpTool("rstream_webtty_server_enrollment_get", "Return the commands needed to enroll and run a registered WebTTY server. The tool does not launch a remote process.", mcpProjectSelectorProperties(map[string]any{"server_id": mcpStringSchema("Registered WebTTY server ID.")}), []string{"server_id"}),
		mcpTool("rstream_webtty_exec", "Execute a non-interactive command through a WebTTY server.", mcpProjectSelectorProperties(map[string]any{"url": mcpStringSchema("WebTTY URL, for example rstrm://shell."), "exec_path": mcpStringSchema("Advertised exec_path from rstream_webtty_list. Defaults to /."), "known_server": mcpStringSchema("Optional local known WebTTY server name for direct URLs."), "command": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1}, "workdir": mcpStringSchema("Optional working directory."), "env": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "user": mcpStringSchema("Optional username or UID.")}), []string{"url", "command"}),
		mcpTool("rstream_webtty_sessions_list", "List managed WebTTY sessions through the local Engine API context.", mcpProjectSelectorProperties(map[string]any{"server_id": mcpStringSchema("Optional registered server ID filter."), "tunnel_id": mcpStringSchema("Optional tunnel ID filter."), "user_id": mcpStringSchema("Optional user ID filter."), "group_id": mcpStringSchema("Optional session group ID filter."), "origin": mcpStringSchema("Optional session origin filter."), "status": mcpStringSchema("Optional session status filter."), "started_after": mcpStringSchema("Optional RFC3339 start lower bound."), "started_before": mcpStringSchema("Optional RFC3339 start upper bound."), "limit": map[string]any{"type": "number", "description": "Optional limit."}}), []string{}),
		mcpTool("rstream_webtty_session_get", "Get metadata for one managed WebTTY session.", mcpProjectSelectorProperties(map[string]any{"session_id": mcpStringSchema("WebTTY session ID.")}), []string{"session_id"}),
		mcpTool("rstream_webtty_session_events", "Read a bounded page of metadata events for one managed WebTTY session.", mcpProjectSelectorProperties(map[string]any{"session_id": mcpStringSchema("WebTTY session ID."), "from_seq": mcpStringSchema("Optional first event sequence."), "limit": map[string]any{"type": "number", "description": "Optional event limit."}}), []string{"session_id"}),
		mcpTool("rstream_webtty_session_export", "Export a managed WebTTY recording as readable text or raw event data. End-to-end encrypted sessions decrypt only when the local trusted device has a valid grant.", mcpProjectSelectorProperties(map[string]any{"session_id": mcpStringSchema("WebTTY session ID."), "format": mcpStringSchema("Export format: text or raw."), "from_seq": mcpStringSchema("Optional first event sequence."), "max_events": map[string]any{"type": "number", "description": "Optional maximum number of events."}, "include_stdin": map[string]any{"type": "boolean", "description": "Include stdin in exports."}, "include_stdout": map[string]any{"type": "boolean", "description": "Include stdout in exports. Defaults to true."}, "include_stderr": map[string]any{"type": "boolean", "description": "Include stderr in exports. Defaults to true."}, "include_timestamps": map[string]any{"type": "boolean", "description": "Include timestamps in text export."}, "include_resize_markers": map[string]any{"type": "boolean", "description": "Include resize markers in text export."}, "terminal_mode_markers": map[string]any{"type": "boolean", "description": "Include terminal mode markers in text export. Defaults to true."}}), []string{"session_id"}),
		mcpTool("rstream_webtty_session_participants", "List participants for one managed WebTTY session.", mcpProjectSelectorProperties(map[string]any{"session_id": mcpStringSchema("WebTTY session ID.")}), []string{"session_id"}),
		mcpTool("rstream_webtty_control_requests_list", "List control-transfer requests for one managed WebTTY session.", mcpProjectSelectorProperties(map[string]any{"session_id": mcpStringSchema("WebTTY session ID."), "status": mcpStringSchema("Optional request status filter."), "requester_user_id": mcpStringSchema("Optional requester user ID filter."), "limit": map[string]any{"type": "number", "description": "Optional limit."}}), []string{"session_id"}),
		mcpTool("rstream_webtty_control_request_create", "Request control of a managed WebTTY session. This never grants control implicitly.", mcpProjectSelectorProperties(map[string]any{"session_id": mcpStringSchema("WebTTY session ID."), "participant_id": mcpStringSchema("Requester participant ID."), "reason": mcpStringSchema("Optional request reason."), "expires_at": mcpStringSchema("Optional RFC3339 expiration time.")}), []string{"session_id", "participant_id"}),
		mcpTool("rstream_webtty_control_request_resolve", "Resolve a pending WebTTY control request as granted, refused, revoked, or expired according to existing authorization rules.", mcpProjectSelectorProperties(map[string]any{"session_id": mcpStringSchema("WebTTY session ID."), "request_id": mcpStringSchema("Control request ID."), "action": mcpStringSchema("Resolution action: grant, refuse, revoke, or expire."), "approver_participant_id": mcpStringSchema("Optional approver participant ID."), "reason": mcpStringSchema("Optional resolution reason.")}), []string{"session_id", "request_id", "action"}),
		mcpTool("rstream_webtty_fs_download", "Download a file from a WebTTY filesystem sidecar to a local Codex-accessible path. Prefer this MCP tool over shelling out to rstream webtty fs download. Remote paths are relative to the server --fs-root.", mcpProjectSelectorProperties(map[string]any{"url": mcpStringSchema("WebTTY URL, for example rstrm://shell."), "fs_path": mcpStringSchema("Advertised fs_path from rstream_webtty_list. Defaults to /fs."), "path": mcpStringSchema("File path inside the advertised filesystem root, for example /logs/latest.tar.gz."), "local_path": mcpStringSchema("Local destination path on the Codex workstation."), "overwrite": map[string]any{"type": "boolean", "description": "Overwrite local_path if it already exists. Defaults to false."}}), []string{"url", "path", "local_path"}),
		mcpTool("rstream_webtty_fs_list", "List a directory exposed by a WebTTY filesystem sidecar. Paths are relative to the server --fs-root; use / for that root, not the host filesystem root.", mcpProjectSelectorProperties(map[string]any{"url": mcpStringSchema("WebTTY URL, for example rstrm://shell."), "fs_path": mcpStringSchema("Advertised fs_path from rstream_webtty_list. Defaults to /fs."), "path": mcpStringSchema("Path inside the advertised filesystem root, for example / or /compose.yaml's parent directory.")}), []string{"url"}),
		mcpTool("rstream_webtty_fs_read", "Read a text or small binary file exposed by a WebTTY filesystem sidecar. For local file artifacts such as archives, prefer rstream_webtty_fs_download. Paths are relative to the server --fs-root.", mcpProjectSelectorProperties(map[string]any{"url": mcpStringSchema("WebTTY URL, for example rstrm://shell."), "fs_path": mcpStringSchema("Advertised fs_path from rstream_webtty_list. Defaults to /fs."), "path": mcpStringSchema("File path inside the advertised filesystem root, for example /README.md."), "encoding": mcpStringSchema("Optional output encoding: text or base64.")}), []string{"url", "path"}),
		mcpTool("rstream_webtty_fs_write", "Write a file through a WebTTY filesystem sidecar. Paths are relative to the server --fs-root; do not pass absolute host paths unless --fs-root is the host root.", mcpProjectSelectorProperties(map[string]any{"url": mcpStringSchema("WebTTY URL, for example rstrm://shell."), "fs_path": mcpStringSchema("Advertised fs_path from rstream_webtty_list. Defaults to /fs."), "path": mcpStringSchema("File path inside the advertised filesystem root, for example /compose.yaml."), "content": mcpStringSchema("File content to write."), "encoding": mcpStringSchema("Optional input encoding: text or base64.")}), []string{"url", "path", "content"}),
		mcpTool("rstream_webtty_fs_mkdir", "Create a directory through a WebTTY filesystem sidecar. Paths are relative to the server --fs-root.", mcpProjectSelectorProperties(map[string]any{"url": mcpStringSchema("WebTTY URL, for example rstrm://shell."), "fs_path": mcpStringSchema("Advertised fs_path from rstream_webtty_list. Defaults to /fs."), "path": mcpStringSchema("Directory path inside the advertised filesystem root.")}), []string{"url", "path"}),
		mcpTool("rstream_webtty_fs_delete", "Delete a file or directory through a WebTTY filesystem sidecar. Paths are relative to the server --fs-root.", mcpProjectSelectorProperties(map[string]any{"url": mcpStringSchema("WebTTY URL, for example rstrm://shell."), "fs_path": mcpStringSchema("Advertised fs_path from rstream_webtty_list. Defaults to /fs."), "path": mcpStringSchema("File or directory path inside the advertised filesystem root.")}), []string{"url", "path"}),
	}
}
func mcpTool(name string, description string, properties map[string]any, required []string) map[string]any {
	behavior, ok := mcpToolBehaviors[name]
	if !ok {
		panic("missing MCP tool behavior for " + name)
	}
	outputSchema := mcpToolOutputSchema(name)
	if len(outputSchema) == 0 {
		panic("missing MCP output schema for " + name)
	}
	return mcpToolFromDefinition(mcpToolDefinition{Name: name, Title: behavior.Title, Description: description, Properties: properties, Required: required, Annotations: mcpToolAnnotations(name), OutputSchema: outputSchema, Meta: mcpToolInvocationMeta(behavior.Title)})
}
func mcpToolFromDefinition(def mcpToolDefinition) map[string]any {
	tool := map[string]any{"name": def.Name, "title": def.Title, "description": def.Description, "inputSchema": map[string]any{"type": "object", "properties": def.Properties, "required": def.Required, "additionalProperties": false}}
	if len(def.Annotations) > 0 {
		tool["annotations"] = def.Annotations
	}
	if len(def.OutputSchema) > 0 {
		tool["outputSchema"] = def.OutputSchema
	}
	if len(def.Meta) > 0 {
		tool["_meta"] = def.Meta
	}
	return tool
}

var mcpToolBehaviors = map[string]mcpToolBehavior{
	"rstream_auth_start":                      {Title: "Start rstream login", OpenWorld: true},
	"rstream_auth_poll":                       {Title: "Finish rstream login", Destructive: true, OpenWorld: true},
	"rstream_context_list":                    {Title: "List rstream contexts", ReadOnly: true, Idempotent: true},
	"rstream_context_get":                     {Title: "Inspect rstream context", ReadOnly: true, Idempotent: true},
	"rstream_project_creation_options":        {Title: "Project Creation Options", ReadOnly: true, Idempotent: true},
	"rstream_project_create":                  {Title: "Create Tunnel Project", OpenWorld: true},
	"rstream_project_update":                  {Title: "Update Tunnel Project", Destructive: true, Idempotent: true, OpenWorld: true},
	"rstream_project_delete":                  {Title: "Delete Tunnel Project", Destructive: true, Idempotent: true, OpenWorld: true},
	"rstream_project_list":                    {Title: "List Tunnel Projects", ReadOnly: true, Idempotent: true},
	"rstream_project_logs":                    {Title: "List Project Logs", ReadOnly: true, Idempotent: true},
	"rstream_project_events_list":             {Title: "List Project Events", ReadOnly: true, Idempotent: true},
	"rstream_project_webhooks_list":           {Title: "List Project Webhooks", ReadOnly: true, Idempotent: true},
	"rstream_project_webhook_deliveries_list": {Title: "List Webhook Deliveries", ReadOnly: true, Idempotent: true},
	"rstream_project_webhook_delivery_get":    {Title: "Get Webhook Delivery", ReadOnly: true, Idempotent: true},
	"rstream_project_usage":                   {Title: "Get Project Usage", ReadOnly: true, Idempotent: true},
	"rstream_project_plan_get":                {Title: "Get Project Plan", ReadOnly: true, Idempotent: true},
	"rstream_project_turn_usage":              {Title: "Get Project TURN Usage", ReadOnly: true, Idempotent: true},
	"rstream_project_turn_credentials_create": {Title: "Create TURN Credentials"},
	"rstream_project_domains_list":            {Title: "List Project Domains", ReadOnly: true, Idempotent: true},
	"rstream_project_domain_create":           {Title: "Create Project Domain", OpenWorld: true},
	"rstream_project_domain_get":              {Title: "Get Project Domain", ReadOnly: true, Idempotent: true},
	"rstream_project_domain_delete":           {Title: "Delete Project Domain", Destructive: true, Idempotent: true, OpenWorld: true},
	"rstream_project_domain_verify":           {Title: "Verify Project Domain", Idempotent: true, OpenWorld: true},
	"rstream_project_domain_connect":          {Title: "Get Domain Connect URL", ReadOnly: true, Idempotent: true, OpenWorld: true},
	"rstream_project_tcp_addresses_list":      {Title: "List project TCP addresses", ReadOnly: true, Idempotent: true, OpenWorld: true},
	"rstream_project_tcp_address_reserve":     {Title: "Reserve project TCP address", OpenWorld: true},
	"rstream_project_tcp_address_release":     {Title: "Release project TCP address", Destructive: true, Idempotent: true, OpenWorld: true},
	"rstream_project_settings_get":            {Title: "Get Project Settings", ReadOnly: true, Idempotent: true},
	"rstream_project_settings_patch":          {Title: "Patch Project Settings", Destructive: true, Idempotent: true, OpenWorld: true},
	"rstream_project_settings_reset":          {Title: "Reset Project Settings", Destructive: true, Idempotent: true, OpenWorld: true},
	"rstream_local_tunnel_expose":             {Title: "Expose local tunnel", OpenWorld: true},
	"rstream_local_tunnel_list":               {Title: "List local tunnels", ReadOnly: true, Idempotent: true},
	"rstream_local_tunnel_stop":               {Title: "Stop local tunnel", Destructive: true, Idempotent: true, OpenWorld: true},
	"rstream_remote_expose":                   {Title: "Expose remote service", OpenWorld: true},
	"rstream_remote_expose_stop":              {Title: "Stop remote exposure", Destructive: true, Idempotent: true, OpenWorld: true},
	"rstream_remote_mcp_discover":             {Title: "Discover remote MCP servers", ReadOnly: true, Idempotent: true, OpenWorld: true},
	"rstream_remote_mcp_tools":                {Title: "List remote MCP tools", ReadOnly: true, Idempotent: true, OpenWorld: true},
	"rstream_remote_mcp_call":                 {Title: "Call remote MCP tool", Destructive: true, OpenWorld: true},
	"rstream_runtime_prepare":                 {Title: "Prepare rstream runtime", Destructive: true, Idempotent: true, OpenWorld: true},
	"rstream_runtime_status":                  {Title: "Check rstream status", ReadOnly: true, Idempotent: true},
	"rstream_token_create":                    {Title: "Create short-lived token"},
	"rstream_workspace_list":                  {Title: "List Workspaces", ReadOnly: true, Idempotent: true},
	"rstream_workspace_members_list":          {Title: "List Workspace Members", ReadOnly: true, Idempotent: true},
	"rstream_webtty_list":                     {Title: "List WebTTY servers", ReadOnly: true, Idempotent: true, OpenWorld: true},
	"rstream_webtty_servers_list":             {Title: "List WebTTY Servers", ReadOnly: true, Idempotent: true},
	"rstream_webtty_server_get":               {Title: "Get WebTTY Server", ReadOnly: true, Idempotent: true},
	"rstream_webtty_server_create":            {Title: "Create Registered WebTTY Server"},
	"rstream_webtty_server_update":            {Title: "Update Registered WebTTY Server", Destructive: true, Idempotent: true},
	"rstream_webtty_server_delete":            {Title: "Delete Registered WebTTY Server", Destructive: true, Idempotent: true},
	"rstream_webtty_server_enrollment_get":    {Title: "Get WebTTY Enrollment", ReadOnly: true, Idempotent: true},
	"rstream_webtty_exec":                     {Title: "Execute WebTTY Command", Destructive: true, OpenWorld: true},
	"rstream_webtty_sessions_list":            {Title: "List WebTTY Sessions", ReadOnly: true, Idempotent: true},
	"rstream_webtty_session_get":              {Title: "Get WebTTY Session", ReadOnly: true, Idempotent: true},
	"rstream_webtty_session_events":           {Title: "List WebTTY Session Events", ReadOnly: true, Idempotent: true},
	"rstream_webtty_session_export":           {Title: "Export WebTTY Session", ReadOnly: true, Idempotent: true},
	"rstream_webtty_session_participants":     {Title: "List WebTTY Participants", ReadOnly: true, Idempotent: true},
	"rstream_webtty_control_requests_list":    {Title: "List WebTTY Control Requests", ReadOnly: true, Idempotent: true},
	"rstream_webtty_control_request_create":   {Title: "Create WebTTY Control Request", OpenWorld: true},
	"rstream_webtty_control_request_resolve":  {Title: "Resolve WebTTY Control Request", Destructive: true, Idempotent: true, OpenWorld: true},
	"rstream_webtty_fs_download":              {Title: "Download remote file", Destructive: true, Idempotent: true, OpenWorld: true},
	"rstream_webtty_fs_list":                  {Title: "List remote files", ReadOnly: true, Idempotent: true, OpenWorld: true},
	"rstream_webtty_fs_read":                  {Title: "Read remote file", ReadOnly: true, Idempotent: true, OpenWorld: true},
	"rstream_webtty_fs_write":                 {Title: "Write remote file", Destructive: true, Idempotent: true, OpenWorld: true},
	"rstream_webtty_fs_mkdir":                 {Title: "Create remote directory", Idempotent: true, OpenWorld: true},
	"rstream_webtty_fs_delete":                {Title: "Delete remote file", Destructive: true, Idempotent: true, OpenWorld: true},
}

func mcpToolAnnotations(name string) map[string]any {
	behavior, ok := mcpToolBehaviors[name]
	if !ok {
		return nil
	}
	return map[string]any{"title": behavior.Title, "readOnlyHint": behavior.ReadOnly, "destructiveHint": behavior.Destructive, "idempotentHint": behavior.Idempotent, "openWorldHint": behavior.OpenWorld}
}

func mcpToolInvocationMeta(title string) map[string]any {
	return map[string]any{
		"openai/toolInvocation/invoking": "Running: " + title,
		"openai/toolInvocation/invoked":  "Finished: " + title,
	}
}

func mcpObjectOutputSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "additionalProperties": true}
	if properties != nil {
		schema["properties"] = properties
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func mcpListOutputSchema(key string, description string) map[string]any {
	return mcpObjectOutputSchema(map[string]any{
		key: map[string]any{"type": "array", "description": description, "items": map[string]any{"type": "object", "additionalProperties": true}},
	}, key)
}

func mcpToolOutputSchema(name string) map[string]any {
	switch name {
	case "rstream_auth_start":
		return map[string]any{"type": "object", "properties": map[string]any{"id": mcpStringSchema("Local auth session ID."), "login_url": mcpStringSchema("User approval URL."), "expires_at": mcpStringSchema("Login request expiry time."), "scopes": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}, "required": []string{"id", "login_url"}, "additionalProperties": true}
	case "rstream_auth_poll":
		return mcpObjectOutputSchema(map[string]any{"authenticated": map[string]any{"type": "boolean"}, "status": mcpStringSchema("Login status."), "id": mcpStringSchema("Local auth session ID."), "next_poll_after_seconds": map[string]any{"type": "number"}, "expires_at": mcpStringSchema("Login request expiry time.")}, "authenticated", "status")
	case "rstream_context_list":
		return mcpListOutputSchema("contexts", "Configured local rstream contexts.")
	case "rstream_context_get", "rstream_project_creation_options", "rstream_project_webhook_delivery_get", "rstream_project_usage", "rstream_project_plan_get", "rstream_project_turn_usage", "rstream_project_turn_credentials_create", "rstream_project_domain_create", "rstream_project_domain_get", "rstream_project_domain_delete", "rstream_project_domain_verify", "rstream_project_domain_connect", "rstream_project_tcp_address_reserve", "rstream_project_tcp_address_release", "rstream_project_settings_get", "rstream_project_settings_patch", "rstream_project_settings_reset", "rstream_local_tunnel_stop":
		return mcpObjectOutputSchema(nil)
	case "rstream_project_create":
		return map[string]any{"type": "object", "properties": map[string]any{"action": mcpStringSchema("Project creation action."), "project": map[string]any{"type": "object"}, "checkout": map[string]any{"type": "object"}}, "required": []string{"action"}, "additionalProperties": true}
	case "rstream_project_update":
		return map[string]any{"type": "object", "properties": map[string]any{"project": map[string]any{"type": "object"}}, "required": []string{"project"}, "additionalProperties": true}
	case "rstream_project_delete":
		return map[string]any{"type": "object", "properties": map[string]any{"action": mcpStringSchema("Project deletion action."), "project_id": mcpStringSchema("Deleted project ID.")}, "required": []string{"action", "project_id"}, "additionalProperties": true}
	case "rstream_project_list":
		return mcpListOutputSchema("projects", "Tunnel projects available to the current account.")
	case "rstream_project_logs", "rstream_project_events_list":
		return mcpListOutputSchema("events", "Project log or lifecycle events.")
	case "rstream_project_webhooks_list":
		return mcpListOutputSchema("webhooks", "Webhook destinations configured on the project.")
	case "rstream_project_webhook_deliveries_list":
		return mcpListOutputSchema("deliveries", "Webhook delivery records.")
	case "rstream_project_domains_list":
		return mcpListOutputSchema("domains", "Stable domains configured on the project.")
	case "rstream_project_tcp_addresses_list":
		return mcpListOutputSchema("addresses", "Reserved TCP addresses configured on the project.")
	case "rstream_runtime_prepare":
		return map[string]any{"type": "object", "properties": map[string]any{"ready": map[string]any{"type": "boolean"}, "needs_login": map[string]any{"type": "boolean"}, "changed": map[string]any{"type": "boolean"}, "project": map[string]any{"type": "object"}, "context": map[string]any{"type": "object"}, "login": map[string]any{"type": "object"}}, "required": []string{"ready"}, "additionalProperties": true}
	case "rstream_runtime_status":
		return map[string]any{"type": "object", "properties": map[string]any{"ready": map[string]any{"type": "boolean"}, "needs_login": map[string]any{"type": "boolean"}, "needs_context": map[string]any{"type": "boolean"}, "suggested_next_tool": mcpStringSchema("Recommended next MCP tool."), "selected_context": mcpStringSchema("Selected local context."), "engine": mcpStringSchema("Resolved engine endpoint."), "agent_guidance": map[string]any{"type": "object", "description": "Non-secret product guidance for SDK selection and self-hosted CE boundaries."}}, "required": []string{"ready"}, "additionalProperties": true}
	case "rstream_local_tunnel_expose":
		return map[string]any{"type": "object", "properties": map[string]any{"id": mcpStringSchema("Local tunnel ID."), "url": mcpStringSchema("Published URL or private rstrm target."), "pid": map[string]any{"type": "number"}, "protocol": mcpStringSchema("Resolved tunnel protocol."), "publish": map[string]any{"type": "boolean"}, "token_auth": map[string]any{"type": "boolean"}, "rstream_auth": map[string]any{"type": "boolean"}, "challenge_mode": map[string]any{"type": "boolean"}, "cleanup_tool": mcpStringSchema("MCP tool to call for cleanup."), "cleanup_id": mcpStringSchema("Local tunnel ID to pass to cleanup_tool.")}, "required": []string{"id", "url", "cleanup_tool", "cleanup_id"}, "additionalProperties": true}
	case "rstream_local_tunnel_list":
		return mcpListOutputSchema("local_tunnels", "Local tunnels managed by this MCP server.")
	case "rstream_remote_expose":
		return mcpObjectOutputSchema(map[string]any{"id": mcpStringSchema("Remote exposure ID."), "status": mcpStringSchema("Remote exposure status."), "command": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "webtty": map[string]any{"type": "object"}}, "id", "status", "command", "webtty")
	case "rstream_remote_expose_stop":
		return mcpObjectOutputSchema(map[string]any{"id": mcpStringSchema("Remote exposure ID."), "exit_code": map[string]any{"type": "number"}, "stdout": mcpStringSchema("Captured stdout."), "stderr": mcpStringSchema("Captured stderr.")}, "id", "exit_code", "stdout", "stderr")
	case "rstream_remote_mcp_discover":
		return mcpListOutputSchema("endpoints", "Discovered remote MCP endpoints.")
	case "rstream_remote_mcp_tools", "rstream_remote_mcp_call":
		return mcpObjectOutputSchema(map[string]any{"jsonrpc": mcpStringSchema("Remote JSON-RPC version."), "id": map[string]any{}, "result": map[string]any{}, "error": map[string]any{"type": "object"}})
	case "rstream_token_create":
		return map[string]any{"type": "object", "properties": map[string]any{"token": mcpStringSchema("Bearer token value."), "token_type": mcpStringSchema("Token type claim."), "expires_at": mcpStringSchema("UTC token expiration time derived from the JWT exp claim."), "ttl_seconds": map[string]any{"type": "number"}, "permissions": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "resources": map[string]any{"type": "object"}, "error": mcpStringSchema("Token creation error.")}, "additionalProperties": true}
	case "rstream_workspace_list":
		return mcpListOutputSchema("workspaces", "Workspaces available to the current account.")
	case "rstream_workspace_members_list":
		return mcpListOutputSchema("members", "Members of the selected workspace.")
	case "rstream_webtty_list":
		return mcpObjectOutputSchema(map[string]any{"servers": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"target": mcpStringSchema("Resolved WebTTY target URL."), "rstream_url": mcpStringSchema("rstrm:// URL when available."), "exec_path": mcpStringSchema("Advertised command execution path."), "fs_path": mcpStringSchema("Advertised filesystem sidecar path.")}, "additionalProperties": true}}}, "servers")
	case "rstream_webtty_servers_list":
		return map[string]any{"type": "object", "properties": map[string]any{"surface": mcpStringSchema("MCP surface name."), "registered": map[string]any{"type": "object"}, "lightweight": map[string]any{"type": "array", "items": map[string]any{"type": "object"}}}, "required": []string{"surface"}, "additionalProperties": true}
	case "rstream_webtty_server_get", "rstream_webtty_server_update":
		return mcpObjectOutputSchema(map[string]any{"surface": mcpStringSchema("MCP surface name."), "server": map[string]any{"type": "object"}}, "surface", "server")
	case "rstream_webtty_server_create", "rstream_webtty_server_enrollment_get":
		return mcpObjectOutputSchema(map[string]any{"surface": mcpStringSchema("MCP surface name."), "server": map[string]any{"type": "object"}, "enrollment": map[string]any{"type": "object"}}, "surface", "server", "enrollment")
	case "rstream_webtty_server_delete":
		return map[string]any{"type": "object", "properties": map[string]any{"surface": mcpStringSchema("MCP surface name."), "deleted": map[string]any{"type": "boolean"}, "server_id": mcpStringSchema("Deleted registered WebTTY server ID.")}, "required": []string{"surface", "deleted", "server_id"}, "additionalProperties": true}
	case "rstream_webtty_exec":
		return map[string]any{"type": "object", "properties": map[string]any{"surface": mcpStringSchema("MCP surface name."), "url": mcpStringSchema("Resolved WebTTY URL."), "command": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "exit_code": map[string]any{"type": "number"}, "stdout": mcpStringSchema("Captured stdout, including partial output on session failure."), "stderr": mcpStringSchema("Captured stderr, including partial output on session failure."), "duration_ms": map[string]any{"type": "number"}, "truncated": map[string]any{"type": "boolean"}, "error": mcpStringSchema("Safe execution error summary."), "error_kind": map[string]any{"type": "string", "enum": []string{"cancelled", "timeout", "session"}}}, "required": []string{"surface", "url", "command", "exit_code", "stdout", "stderr", "duration_ms", "truncated"}, "additionalProperties": true}
	case "rstream_webtty_sessions_list":
		return map[string]any{"type": "object", "properties": map[string]any{"surface": mcpStringSchema("MCP surface name."), "sessions": map[string]any{"type": "array", "items": map[string]any{"type": "object"}}}, "required": []string{"surface", "sessions"}, "additionalProperties": true}
	case "rstream_webtty_session_get":
		return mcpObjectOutputSchema(map[string]any{"surface": mcpStringSchema("MCP surface name."), "session": map[string]any{"type": "object"}}, "surface", "session")
	case "rstream_webtty_session_events":
		return mcpObjectOutputSchema(map[string]any{"surface": mcpStringSchema("MCP surface name."), "events": map[string]any{"type": "array", "items": map[string]any{"type": "object"}}}, "surface", "events")
	case "rstream_webtty_session_export":
		return mcpObjectOutputSchema(map[string]any{"surface": mcpStringSchema("MCP surface name."), "format": mcpStringSchema("Export format."), "session_id": mcpStringSchema("WebTTY session ID."), "text": mcpStringSchema("Rendered text export."), "export": map[string]any{"type": "object"}}, "surface", "format")
	case "rstream_webtty_session_participants":
		return mcpObjectOutputSchema(map[string]any{"surface": mcpStringSchema("MCP surface name."), "participants": map[string]any{"type": "array", "items": map[string]any{"type": "object"}}}, "surface", "participants")
	case "rstream_webtty_control_requests_list":
		return mcpObjectOutputSchema(map[string]any{"surface": mcpStringSchema("MCP surface name."), "control_requests": map[string]any{"type": "array", "items": map[string]any{"type": "object"}}}, "surface", "control_requests")
	case "rstream_webtty_control_request_create", "rstream_webtty_control_request_resolve":
		return mcpObjectOutputSchema(map[string]any{"surface": mcpStringSchema("MCP surface name."), "control_request": map[string]any{"type": "object"}}, "surface", "control_request")
	case "rstream_webtty_fs_list":
		return mcpListOutputSchema("entries", "Directory entries below the advertised filesystem root.")
	case "rstream_webtty_fs_read":
		return map[string]any{"type": "object", "properties": map[string]any{"encoding": mcpStringSchema("Returned encoding."), "content": mcpStringSchema("Returned file content.")}, "required": []string{"encoding", "content"}, "additionalProperties": true}
	case "rstream_webtty_fs_download":
		return map[string]any{"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}, "path": mcpStringSchema("Local destination path."), "bytes": map[string]any{"type": "number"}}, "required": []string{"ok", "path", "bytes"}, "additionalProperties": true}
	case "rstream_webtty_fs_write", "rstream_webtty_fs_mkdir", "rstream_webtty_fs_delete":
		return map[string]any{"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}}, "required": []string{"ok"}, "additionalProperties": true}
	default:
		return nil
	}
}
func mcpStringSchema(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func mcpProjectDomainSelectorProperties() map[string]any {
	return map[string]any{
		"project_id": mcpStringSchema("Tunnel project ID."),
		"domain_id":  mcpStringSchema("Project domain ID. Required unless hostname is provided."),
		"hostname":   mcpStringSchema("Custom hostname. Required unless domain_id is provided."),
	}
}
func handleMCPToolCall(ctx context.Context, params json.RawMessage) (map[string]any, error) {
	var call mcpToolCallParams
	if err := json.Unmarshal(params, &call); err != nil {
		return nil, fmt.Errorf("invalid tools/call params: %w", err)
	}
	switch call.Name {
	case "rstream_auth_start":
		return mcpAuthStart(ctx, call.Arguments)
	case "rstream_auth_poll":
		return mcpAuthPoll(ctx, call.Arguments)
	case "rstream_context_list":
		return mcpContextList(call.Arguments)
	case "rstream_context_get":
		return mcpContextGet(call.Arguments)
	case "rstream_project_creation_options":
		return mcpProjectCreationOptions(ctx, call.Arguments)
	case "rstream_project_create":
		return mcpProjectCreate(ctx, call.Arguments)
	case "rstream_project_update":
		return mcpProjectUpdate(ctx, call.Arguments)
	case "rstream_project_delete":
		return mcpProjectDelete(ctx, call.Arguments)
	case "rstream_project_list":
		return mcpProjectList(ctx, call.Arguments)
	case "rstream_project_logs":
		return mcpProjectLogs(ctx, call.Arguments)
	case "rstream_project_events_list":
		return mcpProjectEventsList(ctx, call.Arguments)
	case "rstream_project_webhooks_list":
		return mcpProjectWebhooksList(ctx, call.Arguments)
	case "rstream_project_webhook_deliveries_list":
		return mcpProjectWebhookDeliveriesList(ctx, call.Arguments)
	case "rstream_project_webhook_delivery_get":
		return mcpProjectWebhookDeliveryGet(ctx, call.Arguments)
	case "rstream_project_usage":
		return mcpProjectUsage(ctx, call.Arguments)
	case "rstream_project_plan_get":
		return mcpProjectPlanGet(ctx, call.Arguments)
	case "rstream_project_turn_usage":
		return mcpProjectTURNUsage(ctx, call.Arguments)
	case "rstream_project_turn_credentials_create":
		return mcpProjectTURNCredentialsCreate(ctx, call.Arguments)
	case "rstream_project_domains_list":
		return mcpProjectDomainsList(ctx, call.Arguments)
	case "rstream_project_domain_create":
		return mcpProjectDomainCreate(ctx, call.Arguments)
	case "rstream_project_domain_get":
		return mcpProjectDomainGet(ctx, call.Arguments)
	case "rstream_project_domain_delete":
		return mcpProjectDomainDelete(ctx, call.Arguments)
	case "rstream_project_domain_verify":
		return mcpProjectDomainVerify(ctx, call.Arguments)
	case "rstream_project_domain_connect":
		return mcpProjectDomainConnect(ctx, call.Arguments)
	case "rstream_project_tcp_addresses_list":
		return mcpProjectTCPAddressesList(ctx, call.Arguments)
	case "rstream_project_tcp_address_reserve":
		return mcpProjectTCPAddressReserve(ctx, call.Arguments)
	case "rstream_project_tcp_address_release":
		return mcpProjectTCPAddressRelease(ctx, call.Arguments)
	case "rstream_project_settings_get":
		return mcpProjectSettingsGet(ctx, call.Arguments)
	case "rstream_project_settings_patch":
		return mcpProjectSettingsPatch(ctx, call.Arguments)
	case "rstream_project_settings_reset":
		return mcpProjectSettingsReset(ctx, call.Arguments)
	case "rstream_local_tunnel_expose":
		return mcpLocalTunnelExpose(ctx, call.Arguments)
	case "rstream_local_tunnel_list":
		return mcpLocalTunnelList()
	case "rstream_local_tunnel_stop":
		return mcpLocalTunnelStop(call.Arguments)
	case "rstream_remote_expose":
		return mcpRemoteExpose(ctx, call.Arguments)
	case "rstream_remote_expose_stop":
		return mcpRemoteExposeStop(ctx, call.Arguments)
	case "rstream_remote_mcp_discover":
		return mcpRemoteMCPDiscover(ctx, call.Arguments)
	case "rstream_remote_mcp_tools":
		return mcpRemoteMCPTools(ctx, call.Arguments)
	case "rstream_remote_mcp_call":
		return mcpRemoteMCPCall(ctx, call.Arguments)
	case "rstream_runtime_prepare":
		return mcpRuntimePrepare(ctx, call.Arguments)
	case "rstream_runtime_status":
		return mcpRuntimeStatus(ctx)
	case "rstream_token_create":
		return mcpTokenCreate(ctx, call.Arguments)
	case "rstream_workspace_list":
		return mcpWorkspaceList(ctx, call.Arguments)
	case "rstream_workspace_members_list":
		return mcpWorkspaceMembersList(ctx, call.Arguments)
	case "rstream_webtty_list":
		return mcpWebTTYList(ctx, call.Arguments)
	case "rstream_webtty_servers_list":
		return mcpWebTTYServersList(ctx, call.Arguments)
	case "rstream_webtty_server_get":
		return mcpWebTTYServerGet(ctx, call.Arguments)
	case "rstream_webtty_server_create":
		return mcpWebTTYServerCreate(ctx, call.Arguments)
	case "rstream_webtty_server_update":
		return mcpWebTTYServerUpdate(ctx, call.Arguments)
	case "rstream_webtty_server_delete":
		return mcpWebTTYServerDelete(ctx, call.Arguments)
	case "rstream_webtty_server_enrollment_get":
		return mcpWebTTYServerEnrollmentGet(ctx, call.Arguments)
	case "rstream_webtty_exec":
		return mcpWebTTYExec(ctx, call.Arguments)
	case "rstream_webtty_sessions_list":
		return mcpWebTTYSessionsList(ctx, call.Arguments)
	case "rstream_webtty_session_get":
		return mcpWebTTYSessionGet(ctx, call.Arguments)
	case "rstream_webtty_session_events":
		return mcpWebTTYSessionEvents(ctx, call.Arguments)
	case "rstream_webtty_session_export":
		return mcpWebTTYSessionExport(ctx, call.Arguments)
	case "rstream_webtty_session_participants":
		return mcpWebTTYSessionParticipants(ctx, call.Arguments)
	case "rstream_webtty_control_requests_list":
		return mcpWebTTYControlRequestsList(ctx, call.Arguments)
	case "rstream_webtty_control_request_create":
		return mcpWebTTYControlRequestCreate(ctx, call.Arguments)
	case "rstream_webtty_control_request_resolve":
		return mcpWebTTYControlRequestResolve(ctx, call.Arguments)
	case "rstream_webtty_fs_list":
		return mcpWebTTYFSList(ctx, call.Arguments)
	case "rstream_webtty_fs_read":
		return mcpWebTTYFSRead(ctx, call.Arguments)
	case "rstream_webtty_fs_download":
		return mcpWebTTYFSDownload(ctx, call.Arguments)
	case "rstream_webtty_fs_write":
		return mcpWebTTYFSWrite(ctx, call.Arguments)
	case "rstream_webtty_fs_mkdir":
		return mcpWebTTYFSMkdir(ctx, call.Arguments)
	case "rstream_webtty_fs_delete":
		return mcpWebTTYFSDelete(ctx, call.Arguments)
	default:
		return nil, fmt.Errorf("unknown tool %q", call.Name)
	}
}
func mcpWebTTYList(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	runtime, err := resolveMCPRuntimeForArgs(ctx, args)
	if err != nil {
		return nil, err
	}
	client, err := newClientFromResolved(runtime.Resolved)
	if err != nil {
		return nil, err
	}
	defer closeRstreamClientLogged(client, slog.Default())
	filter, nameFilter, err := mcpWebTTYFilterArgs(args)
	if err != nil {
		return nil, err
	}
	servers, err := listWebTTYServers(ctx, client, filter)
	if err != nil {
		return nil, err
	}
	servers = filterMCPWebTTYServers(servers, nameFilter)
	return mcpJSONResult(map[string]any{"servers": servers}, false)
}
func mcpWebTTYFilterArgs(args map[string]json.RawMessage) (string, string, error) {
	name, err := mcpOptionalStringArg(args, "name", "")
	if err != nil {
		return "", "", err
	}
	filter, err := mcpOptionalStringArg(args, "filter", "")
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(filter) != "" && !strings.Contains(filter, "=") {
		name = combineMCPFilters(name, filter)
		filter = ""
	}
	return filter, name, nil
}
func combineMCPFilters(left string, right string) string {
	if strings.TrimSpace(left) == "" {
		return right
	}
	if strings.TrimSpace(right) == "" {
		return left
	}
	return left + "," + right
}
func filterMCPWebTTYServers(servers []webtty.ServerInfo, nameFilter string) []webtty.ServerInfo {
	if strings.TrimSpace(nameFilter) == "" {
		return servers
	}
	names := strings.Split(nameFilter, ",")
	filtered := []webtty.ServerInfo{}
	for _, server := range servers {
		if mcpWebTTYServerMatchesName(server, names) {
			filtered = append(filtered, server)
		}
	}
	return filtered
}
func mcpWebTTYServerMatchesName(server webtty.ServerInfo, names []string) bool {
	fields := []string{server.Target, server.RstreamURL}
	if server.TunnelName != nil {
		fields = append(fields, *server.TunnelName)
	}
	if server.ServerID != nil {
		fields = append(fields, *server.ServerID)
	}
	if server.HostKeyID != nil {
		fields = append(fields, *server.HostKeyID)
	}
	if server.Hostname != nil {
		fields = append(fields, *server.Hostname)
	}
	for _, name := range names {
		needle := strings.ToLower(strings.TrimSpace(name))
		if needle == "" {
			continue
		}
		for _, field := range fields {
			if strings.Contains(strings.ToLower(field), needle) {
				return true
			}
		}
	}
	return false
}
func mcpWebTTYExec(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	command, err := mcpRequiredStringSliceArg(args, "command")
	if err != nil {
		return nil, err
	}
	cfg, err := mcpWebTTYExecClientConfig(ctx, args, command)
	if err != nil {
		return nil, err
	}
	result, runErr := runWebTTYClientCapture(ctx, cfg.ClientConfig)
	err = errors.Join(runErr, cfg.Close())
	return mcpWebTTYExecResult(result, err)
}

func mcpWebTTYExecResult(result *webTTYClientResult, runErr error) (map[string]any, error) {
	if result == nil {
		if runErr != nil {
			return nil, runErr
		}
		return nil, errors.New("webtty execution returned no result")
	}
	payload := map[string]any{
		"surface":     "cli",
		"url":         result.URL,
		"command":     result.Command,
		"exit_code":   result.ExitCode,
		"stdout":      result.Stdout,
		"stderr":      result.Stderr,
		"duration_ms": result.DurationMS,
		"truncated":   false,
	}
	if runErr != nil {
		payload["error"] = "WebTTY command execution failed."
		payload["error_kind"] = mcpWebTTYExecErrorKind(runErr)
	}
	return mcpJSONResult(payload, runErr != nil || result.ExitCode != 0)
}

func mcpWebTTYExecErrorKind(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	default:
		return "session"
	}
}

type mcpWebTTYClientConfig struct {
	*webtty.ClientConfig
	rstreamClient *ownedRstreamClient
}

func (c *mcpWebTTYClientConfig) Close() error {
	if c == nil {
		return nil
	}
	return c.rstreamClient.Close()
}

func mcpWebTTYExecClientConfig(ctx context.Context, args map[string]json.RawMessage, command []string) (result *mcpWebTTYClientConfig, err error) {
	urlValue, err := mcpRequiredStringArg(args, "url")
	if err != nil {
		return nil, err
	}
	execPath, err := mcpOptionalStringArg(args, "exec_path", "")
	if err != nil {
		return nil, err
	}
	urlValue, err = resolveWebTTYExecURL(urlValue, execPath)
	if err != nil {
		return nil, err
	}
	envVars, err := mcpOptionalStringSliceArg(args, "env")
	if err != nil {
		return nil, err
	}
	workdir, err := mcpOptionalStringPtrArg(args, "workdir")
	if err != nil {
		return nil, err
	}
	username, err := mcpOptionalStringPtrArg(args, "user")
	if err != nil {
		return nil, err
	}
	knownServer, err := mcpOptionalStringArg(args, "known_server", "")
	if err != nil {
		return nil, err
	}
	result = &mcpWebTTYClientConfig{ClientConfig: &webtty.ClientConfig{URL: urlValue, Interactive: false, AllocateTTY: false, SendHeartbeat: true, EnvVars: envVars, Workdir: workdir, Username: username, CmdArgs: command}}
	defer func() {
		if err != nil {
			err = errors.Join(err, result.Close())
			result = nil
		}
	}()
	var runtimeE2E *webTTYClientRuntimeE2EContext
	var securityScope webTTYClientSecurityScope
	if webttyClientUsesRstream(urlValue) {
		runtime, err := resolveMCPRuntimeForArgs(ctx, args)
		if err != nil {
			return nil, err
		}
		client, err := newClientFromResolved(runtime.Resolved)
		if err != nil {
			return nil, err
		}
		result.rstreamClient = ownRstreamClient(client)
		rstreamResolution, err := resolveWebTTYClientRstream(ctx, runtime, client, urlValue)
		if err != nil {
			return nil, err
		}
		urlValue = rstreamResolution.URL
		result.URL = urlValue
		runtimeE2E = rstreamResolution.RuntimeE2E
		securityScope = rstreamResolution.Scope
		result.DialContext = newWebTTYClientDialContext(client)
	}
	sources, serverKeysConfigured, err := webTTYKnownServerSourcesFromMCPEnvironment(knownServer)
	if err != nil {
		return nil, err
	}
	cryptoConfig, err := webTTYClientCryptoFromSources(ctx, false, sources, serverKeysConfigured, runtimeE2E, securityScope)
	if err != nil {
		return nil, err
	}
	result.PayloadCrypto = cryptoConfig.PayloadCrypto
	result.EndpointIdentity = cryptoConfig.EndpointIdentity
	if cryptoConfig.ExpectedServerIdentity != nil && result.EndpointIdentity == nil && strings.TrimSpace(cryptoConfig.ClientIdentityName) != "" {
		result.EndpointIdentity, err = webTTYClientEndpointIdentityByName(cryptoConfig.ClientIdentityName)
		if err != nil {
			return nil, err
		}
	}
	if cryptoConfig.ExpectedServerIdentity != nil && result.EndpointIdentity == nil {
		result.EndpointIdentity, _, err = webTTYClientEndpointIdentityFromExplicitSources(nil)
		if err != nil {
			return nil, err
		}
	}
	if cryptoConfig.ExpectedServerIdentity != nil && result.EndpointIdentity == nil {
		return nil, webTTYMissingClientEndpointIdentityError(securityScope, cryptoConfig)
	}
	result.ExpectedServerIdentity = cryptoConfig.ExpectedServerIdentity
	return result, nil
}
func webTTYKnownServerSourcesFromMCPEnvironment(knownServer string) ([]webTTYKnownServerSource, bool, error) {
	knownServer = strings.TrimSpace(knownServer)
	if knownServer == "" {
		return webTTYKnownServerSourcesFromEnvironment()
	}
	if strings.TrimSpace(os.Getenv(webTTYKnownServerKeyEnv)) != "" {
		return nil, true, fmt.Errorf("known_server cannot be combined with %s", webTTYKnownServerKeyEnv)
	}
	path := strings.TrimSpace(os.Getenv(webTTYKnownServersFileEnv))
	pathSet := path != ""
	if pathSet {
		var err error
		path, err = expandWebTTYPath(path)
		if err != nil {
			return nil, true, err
		}
	}
	source, err := webTTYKnownServerSourceFromLocalStore(knownServer, path, pathSet)
	if err != nil {
		return nil, true, err
	}
	return []webTTYKnownServerSource{source}, true, nil
}
func mcpWebTTYFSList(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	client, err := newWebTTYFSMCPClient(ctx, args)
	if err != nil {
		return nil, err
	}
	defer closeWebTTYFSClientLogged(client)
	remotePath, err := mcpOptionalStringArg(args, "path", "/")
	if err != nil {
		return nil, err
	}
	items, err := client.list(ctx, remotePath)
	if err != nil {
		return nil, err
	}
	return mcpJSONResult(map[string]any{"entries": items}, false)
}
func mcpWebTTYFSRead(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	client, err := newWebTTYFSMCPClient(ctx, args)
	if err != nil {
		return nil, err
	}
	defer closeWebTTYFSClientLogged(client)
	remotePath, err := mcpRequiredStringArg(args, "path")
	if err != nil {
		return nil, err
	}
	buffer := bytes.Buffer{}
	if err := client.read(ctx, remotePath, &buffer); err != nil {
		return nil, err
	}
	encoding, err := mcpOptionalStringArg(args, "encoding", "text")
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "text":
		result := mcpToolTextResult(buffer.String(), false)
		result["structuredContent"] = map[string]any{"encoding": "text", "content": buffer.String()}
		return result, nil
	case "base64":
		return mcpJSONResult(map[string]string{"encoding": "base64", "content": base64.StdEncoding.EncodeToString(buffer.Bytes())}, false)
	default:
		return nil, fmt.Errorf("invalid encoding %q (valid: text, base64)", encoding)
	}
}
func mcpWebTTYFSDownload(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	client, err := newWebTTYFSMCPClient(ctx, args)
	if err != nil {
		return nil, err
	}
	defer closeWebTTYFSClientLogged(client)
	remotePath, err := mcpRequiredStringArg(args, "path")
	if err != nil {
		return nil, err
	}
	localPath, err := mcpRequiredStringArg(args, "local_path")
	if err != nil {
		return nil, err
	}
	overwrite, err := mcpOptionalBoolArg(args, "overwrite", false)
	if err != nil {
		return nil, err
	}
	if err := prepareMCPDownloadPath(localPath, overwrite); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(localPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if err := client.read(ctx, remotePath, file); err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	return mcpJSONResult(map[string]any{"ok": true, "path": localPath, "bytes": info.Size()}, false)
}
func mcpWebTTYFSWrite(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	client, err := newWebTTYFSMCPClient(ctx, args)
	if err != nil {
		return nil, err
	}
	defer closeWebTTYFSClientLogged(client)
	remotePath, err := mcpRequiredStringArg(args, "path")
	if err != nil {
		return nil, err
	}
	content, err := mcpStringArg(args, "content")
	if err != nil {
		return nil, err
	}
	reader, err := mcpContentReader(args, content)
	if err != nil {
		return nil, err
	}
	if err := client.write(ctx, remotePath, reader); err != nil {
		return nil, err
	}
	return mcpJSONResult(map[string]bool{"ok": true}, false)
}
func mcpWebTTYFSMkdir(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	client, err := newWebTTYFSMCPClient(ctx, args)
	if err != nil {
		return nil, err
	}
	defer closeWebTTYFSClientLogged(client)
	remotePath, err := mcpRequiredStringArg(args, "path")
	if err != nil {
		return nil, err
	}
	if err := client.mkcol(ctx, remotePath); err != nil {
		return nil, err
	}
	return mcpJSONResult(map[string]bool{"ok": true}, false)
}
func mcpWebTTYFSDelete(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	client, err := newWebTTYFSMCPClient(ctx, args)
	if err != nil {
		return nil, err
	}
	defer closeWebTTYFSClientLogged(client)
	remotePath, err := mcpRequiredStringArg(args, "path")
	if err != nil {
		return nil, err
	}
	if err := client.delete(ctx, remotePath); err != nil {
		return nil, err
	}
	return mcpJSONResult(map[string]bool{"ok": true}, false)
}
func prepareMCPDownloadPath(localPath string, overwrite bool) error {
	if strings.TrimSpace(localPath) == "" {
		return fmt.Errorf("argument %q must not be empty", "local_path")
	}
	if !overwrite {
		if _, err := os.Stat(localPath); err == nil {
			return fmt.Errorf("local_path %q already exists; pass overwrite=true to replace it", localPath)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	parent := filepath.Dir(localPath)
	if parent == "." || parent == "" {
		return nil
	}
	return os.MkdirAll(parent, 0o700)
}
func mcpContentReader(args map[string]json.RawMessage, content string) (io.Reader, error) {
	encoding, err := mcpOptionalStringArg(args, "encoding", "text")
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "text":
		return strings.NewReader(content), nil
	case "base64":
		decoded, err := base64.StdEncoding.DecodeString(content)
		if err != nil {
			return nil, fmt.Errorf("invalid base64 content: %w", err)
		}
		return bytes.NewReader(decoded), nil
	default:
		return nil, fmt.Errorf("invalid encoding %q (valid: text, base64)", encoding)
	}
}
func newWebTTYFSMCPClient(ctx context.Context, args map[string]json.RawMessage) (result *webTTYFSClient, err error) {
	rawURL, err := mcpRequiredStringArg(args, "url")
	if err != nil {
		return nil, err
	}
	fsPath, err := mcpOptionalStringArg(args, "fs_path", "")
	if err != nil {
		return nil, err
	}
	baseURL, target, err := resolveWebTTYFSBaseURL(rawURL, fsPath)
	if err != nil {
		return nil, err
	}
	httpClient := http.DefaultClient
	var rstreamClient *ownedRstreamClient
	defer func() {
		if err != nil {
			if closeErr := rstreamClient.Close(); closeErr != nil {
				slog.Warn("failed to close MCP WebTTY filesystem client", "error", closeErr)
			}
		}
	}()
	if target != "" {
		runtime, err := resolveMCPRuntimeForArgs(ctx, args)
		if err != nil {
			return nil, err
		}
		client, err := newClientFromResolved(runtime.Resolved)
		if err != nil {
			return nil, err
		}
		rstreamClient = ownRstreamClient(client)
		httpClient = &http.Client{Transport: &http.Transport{DialContext: newWebTTYFSDialContext(client, target)}}
	}
	return &webTTYFSClient{client: httpClient, baseURL: baseURL, rstreamClient: rstreamClient}, nil
}
func resolveMCPRuntime(ctx context.Context, requireEngine bool, requireToken bool) (*resolvedRuntime, error) {
	env := config.ReadEnv()
	path := env.ConfigPath
	if path == "" {
		var err error
		path, err = config.DefaultConfigPath()
		if err != nil {
			return nil, err
		}
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	envAPIURL := env.APIURL
	if envAPIURL == "" && env.Context == "" && cfg.Defaults.Context == nil && len(cfg.Environments) == 1 {
		envAPIURL = cfg.Environments[0].APIURL
	}
	input := config.ResolveInput{Config: cfg, EnvAPIURL: envAPIURL, EnvContext: env.Context, EnvEngine: env.Engine, EnvToken: env.Token, EnvMTLSCert: env.MTLSCert, EnvMTLSKey: env.MTLSKey, EnvRegion: env.Region, EnvTunnelTransport: env.TunnelTransport, EnvUseQUIC: env.UseQUIC, EnvControlPlaneHeaders: env.ControlPlaneHeaders, RequireEngine: requireEngine, RequireToken: requireToken, ResolveToken: true}
	resolved, err := config.Resolve(input)
	if err != nil {
		return nil, err
	}
	if requireEngine && resolved.Region != "" {
		if err := resolveRuntimeRegionContext(ctx, cfg, &resolved); err != nil {
			return nil, err
		}
	}
	return &resolvedRuntime{ConfigPath: path, Config: cfg, Resolved: resolved}, nil
}
func mcpRequiredStringArg(args map[string]json.RawMessage, name string) (string, error) {
	value, err := mcpOptionalStringArg(args, name, "")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("missing required argument %q", name)
	}
	return value, nil
}
func mcpStringArg(args map[string]json.RawMessage, name string) (string, error) {
	raw, ok := args[name]
	if !ok {
		return "", fmt.Errorf("missing required argument %q", name)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("argument %q must be a string", name)
	}
	return value, nil
}
func mcpOptionalStringArg(args map[string]json.RawMessage, name string, fallback string) (string, error) {
	raw, ok := args[name]
	if !ok {
		return fallback, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("argument %q must be a string", name)
	}
	if strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	return value, nil
}
func mcpOptionalStringPtrArg(args map[string]json.RawMessage, name string) (*string, error) {
	value, err := mcpOptionalStringArg(args, name, "")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	return &value, nil
}
func mcpOptionalBoolArg(args map[string]json.RawMessage, name string, fallback bool) (bool, error) {
	raw, ok := args[name]
	if !ok {
		return fallback, nil
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, fmt.Errorf("argument %q must be a boolean", name)
	}
	return value, nil
}
func mcpOptionalBoolPtrArg(args map[string]json.RawMessage, name string) (*bool, error) {
	raw, ok := args[name]
	if !ok {
		return nil, nil
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("argument %q must be a boolean", name)
	}
	return &value, nil
}
func mcpOptionalIntArg(args map[string]json.RawMessage, name string) (*int, error) {
	raw, ok := args[name]
	if !ok {
		return nil, nil
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("argument %q must be a number", name)
	}
	return &value, nil
}
func mcpRequiredStringSliceArg(args map[string]json.RawMessage, name string) ([]string, error) {
	values, err := mcpOptionalStringSliceArg(args, name)
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("missing required argument %q", name)
	}
	return values, nil
}
func mcpOptionalStringSliceArg(args map[string]json.RawMessage, name string) ([]string, error) {
	raw, ok := args[name]
	if !ok {
		return nil, nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("argument %q must be an array of strings", name)
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out, nil
}
func mcpJSONResult(value any, isError bool) (map[string]any, error) {
	structuredContent, err := mcpStructuredContent(value)
	if err != nil {
		return nil, err
	}
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	result := mcpToolTextResult(string(payload), isError)
	result["structuredContent"] = structuredContent
	return result, nil
}
func mcpJSONResourceLinkResult(value any, isError bool, uri string, name string, description string, mimeType string) (map[string]any, error) {
	result, err := mcpJSONResult(value, isError)
	if err != nil {
		return nil, err
	}
	if !isError && strings.TrimSpace(uri) != "" {
		result["content"] = append(result["content"].([]map[string]any), map[string]any{"type": "resource_link", "uri": uri, "name": name, "description": description, "mimeType": mimeType})
	}
	return result, nil
}
func mcpToolTextResult(text string, isError bool) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": isError}
}
func mcpStructuredContent(value any) (map[string]any, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, fmt.Errorf("structured content must be a JSON object: %w", err)
	}
	if decoded == nil {
		return nil, errors.New("structured content must be a JSON object")
	}
	return decoded, nil
}
