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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/rstreamlabs/rstream-go/webtty"
	"github.com/spf13/cobra"
)

type failingMCPWriter struct {
	err error
}

func (w failingMCPWriter) Write(_ []byte) (int, error) {
	return 0, w.err
}

type blockingMCPReader struct {
	closed    chan struct{}
	started   chan struct{}
	closeOnce sync.Once
	startOnce sync.Once
}

type blockingMCPWriter struct {
	closed    chan struct{}
	started   chan struct{}
	closeOnce sync.Once
	startOnce sync.Once
}

type lockedMCPBuffer struct {
	buffer bytes.Buffer
	mu     sync.Mutex
}

func newBlockingMCPReader() *blockingMCPReader {
	return &blockingMCPReader{closed: make(chan struct{}), started: make(chan struct{})}
}

func (r *blockingMCPReader) Read(_ []byte) (int, error) {
	r.startOnce.Do(func() { close(r.started) })
	<-r.closed
	return 0, io.ErrClosedPipe
}

func (r *blockingMCPReader) Close() error {
	r.closeOnce.Do(func() { close(r.closed) })
	return nil
}

func newBlockingMCPWriter() *blockingMCPWriter {
	return &blockingMCPWriter{closed: make(chan struct{}), started: make(chan struct{})}
}

func (w *blockingMCPWriter) Write(_ []byte) (int, error) {
	w.startOnce.Do(func() { close(w.started) })
	<-w.closed
	return 0, io.ErrClosedPipe
}

func (w *blockingMCPWriter) Close() error {
	w.closeOnce.Do(func() { close(w.closed) })
	return nil
}

func (b *lockedMCPBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(value)
}

func (b *lockedMCPBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func TestMCPReadWriteFraming(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	framed := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(input), input)
	message, err := readMCPMessage(bufio.NewReader(strings.NewReader(framed)))
	if err != nil {
		t.Fatalf("readMCPMessage returned error: %v", err)
	}
	if message.Method != "tools/list" || string(message.ID) != "1" {
		t.Fatalf("unexpected message: %#v", message)
	}
	var output bytes.Buffer
	if err := writeMCPResponse(&output, mcpResponse{JSONRPC: "2.0", ID: message.ID, Result: map[string]string{"ok": "true"}}); err != nil {
		t.Fatalf("writeMCPResponse returned error: %v", err)
	}
	if !strings.HasPrefix(output.String(), "Content-Length: ") || !strings.Contains(output.String(), `"jsonrpc":"2.0"`) {
		t.Fatalf("unexpected framed response: %q", output.String())
	}
}

func TestMCPReadLineDelimitedJSON(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":0,"method":"initialize"}` + "\n"
	message, err := readMCPMessage(bufio.NewReader(strings.NewReader(input)))
	if err != nil {
		t.Fatalf("readMCPMessage returned error: %v", err)
	}
	if message.Method != "initialize" || string(message.ID) != "0" {
		t.Fatalf("unexpected message: %#v", message)
	}
	var output bytes.Buffer
	if err := writeMCPResponseWithFraming(&output, mcpResponse{JSONRPC: "2.0", ID: message.ID, Result: map[string]string{"ok": "true"}}, mcpFramingLineDelimited); err != nil {
		t.Fatalf("writeMCPResponseWithFraming returned error: %v", err)
	}
	if !strings.HasSuffix(output.String(), "\n") || strings.HasPrefix(output.String(), "Content-Length:") || !strings.Contains(output.String(), `"ok":"true"`) {
		t.Fatalf("unexpected line-delimited response: %q", output.String())
	}
}

func TestServeMCPRecoversAfterInvalidJSONRPCMessages(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":`,
		`[]`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		"",
	}, "\n")
	var output bytes.Buffer
	if err := serveMCP(t.Context(), strings.NewReader(input), &output); err != nil {
		t.Fatalf("serveMCP returned error: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("serveMCP wrote %d responses, want 3: %q", len(lines), output.String())
	}
	var responses []mcpResponse
	for _, line := range lines {
		var response mcpResponse
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			t.Fatalf("response is not valid JSON: %v (%q)", err, line)
		}
		responses = append(responses, response)
	}
	if responses[0].Error == nil || responses[0].Error.Code != -32700 || string(responses[0].ID) != "null" {
		t.Fatalf("malformed JSON response = %#v", responses[0])
	}
	if responses[1].Error == nil || responses[1].Error.Code != -32600 || string(responses[1].ID) != "null" {
		t.Fatalf("invalid request response = %#v", responses[1])
	}
	if responses[2].Error != nil || responses[2].Result == nil || string(responses[2].ID) != "1" {
		t.Fatalf("tools/list response = %#v", responses[2])
	}
}

func TestMCPRejectsInvalidRequestShape(t *testing.T) {
	for _, message := range []mcpMessage{
		{JSONRPC: "1.0", ID: json.RawMessage("1"), Method: "tools/list"},
		{JSONRPC: "2.0", ID: json.RawMessage("true"), Method: "tools/list"},
		{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/call", Params: json.RawMessage(`[]`)},
	} {
		response := handleMCPMessage(t.Context(), message)
		if response.Error == nil || response.Error.Code != -32600 || string(response.ID) != "null" {
			t.Fatalf("invalid message response = %#v", response)
		}
	}
}

func TestServeMCPReturnsWhenContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	inputReader, inputWriter := io.Pipe()
	defer inputReader.Close()
	defer inputWriter.Close()
	done := make(chan error, 1)
	go func() { done <- serveMCP(ctx, inputReader, &bytes.Buffer{}) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveMCP returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("serveMCP did not return after context cancellation")
	}
}

func TestServeMCPCancellationClosesBlockingInput(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	input := newBlockingMCPReader()
	done := make(chan error, 1)
	go func() { done <- serveMCP(ctx, input, &bytes.Buffer{}) }()
	select {
	case <-input.started:
	case <-time.After(time.Second):
		t.Fatal("MCP input read did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveMCP returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("serveMCP did not return after closing blocking input")
	}
	select {
	case <-input.closed:
	default:
		t.Fatal("MCP input was not closed")
	}
}

func TestServeMCPCancellationClosesBlockingOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	output := newBlockingMCPWriter()
	done := make(chan error, 1)
	go func() {
		done <- serveMCP(ctx, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`+"\n"), output)
	}()
	select {
	case <-output.started:
	case <-time.After(time.Second):
		t.Fatal("MCP output write did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveMCP returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("serveMCP did not return after closing blocking output")
	}
	select {
	case <-output.closed:
	default:
		t.Fatal("MCP output was not closed")
	}
}

func TestServeMCPRunsRequestsConcurrentlyWithinBounds(t *testing.T) {
	inputReader, inputWriter := io.Pipe()
	defer inputReader.Close()
	release := make(chan struct{})
	started := make(chan struct{}, mcpMaxConcurrentRequests+mcpMaxQueuedRequests)
	var active atomic.Int32
	var maximum atomic.Int32
	var handled atomic.Int32
	handler := func(_ context.Context, message mcpMessage) mcpResponse {
		current := active.Add(1)
		for previous := maximum.Load(); current > previous && !maximum.CompareAndSwap(previous, current); previous = maximum.Load() {
		}
		handled.Add(1)
		started <- struct{}{}
		<-release
		active.Add(-1)
		return mcpResponse{JSONRPC: "2.0", ID: message.ID, Result: map[string]bool{"ok": true}}
	}
	var output lockedMCPBuffer
	done := make(chan error, 1)
	go func() { done <- serveMCPWithHandler(t.Context(), inputReader, &output, handler) }()
	writeDone := make(chan error, 1)
	go func() {
		for id := 1; id <= mcpMaxConcurrentRequests; id++ {
			if _, err := fmt.Fprintf(inputWriter, `{"jsonrpc":"2.0","id":%d,"method":"tools/list"}`+"\n", id); err != nil {
				writeDone <- err
				return
			}
		}
		for queued := range mcpMaxQueuedRequests + 1 {
			id := mcpMaxConcurrentRequests + queued + 1
			if _, err := fmt.Fprintf(inputWriter, `{"jsonrpc":"2.0","id":%d,"method":"tools/list"}`+"\n", id); err != nil {
				writeDone <- err
				return
			}
		}
		writeDone <- inputWriter.Close()
	}()
	for range mcpMaxConcurrentRequests {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("concurrent MCP request did not start")
		}
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("write MCP requests: %v", err)
	}
	select {
	case <-started:
		t.Fatal("MCP server exceeded the concurrent request bound")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveMCPWithHandler returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("MCP server did not drain bounded requests")
	}
	if got := maximum.Load(); got != mcpMaxConcurrentRequests {
		t.Fatalf("maximum concurrent requests = %d, want %d", got, mcpMaxConcurrentRequests)
	}
	if got := handled.Load(); got > mcpMaxConcurrentRequests+mcpMaxQueuedRequests {
		t.Fatalf("handled requests = %d, exceeds worker plus queue bound", got)
	}
	if !strings.Contains(output.String(), `"code":-32000`) {
		t.Fatalf("overload response is missing: %q", output.String())
	}
}

func TestServeMCPCancellationStopsRequestWithoutResponse(t *testing.T) {
	inputReader, inputWriter := io.Pipe()
	defer inputReader.Close()
	started := make(chan struct{})
	cancelled := make(chan struct{})
	handler := func(ctx context.Context, message mcpMessage) mcpResponse {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return mcpResponse{JSONRPC: "2.0", ID: message.ID, Result: map[string]bool{"late": true}}
	}
	var output bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- serveMCPWithHandler(t.Context(), inputReader, &output, handler) }()
	if _, err := io.WriteString(inputWriter, `{"jsonrpc":"2.0","id":"slow","method":"tools/list"}`+"\n"); err != nil {
		t.Fatalf("write MCP request: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("MCP request did not start")
	}
	if _, err := io.WriteString(inputWriter, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"slow","reason":"test"}}`+"\n"); err != nil {
		t.Fatalf("write MCP cancellation: %v", err)
	}
	if err := inputWriter.Close(); err != nil {
		t.Fatalf("close MCP input: %v", err)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("MCP cancellation did not reach the request context")
	}
	if err := <-done; err != nil {
		t.Fatalf("serveMCPWithHandler returned error: %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("cancelled MCP request wrote a response: %q", output.String())
	}
}

func TestServeMCPCancellationRemovesQueuedRequest(t *testing.T) {
	inputReader, inputWriter := io.Pipe()
	defer inputReader.Close()
	release := make(chan struct{})
	started := make(chan string, mcpMaxConcurrentRequests+1)
	handler := func(ctx context.Context, message mcpMessage) mcpResponse {
		started <- string(message.ID)
		if string(message.ID) == `"queued"` {
			return mcpResponse{JSONRPC: "2.0", ID: message.ID, Result: map[string]bool{"late": true}}
		}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return mcpResponse{JSONRPC: "2.0", ID: message.ID, Result: map[string]bool{"ok": true}}
	}
	var output lockedMCPBuffer
	done := make(chan error, 1)
	go func() { done <- serveMCPWithHandler(t.Context(), inputReader, &output, handler) }()
	for id := 1; id <= mcpMaxConcurrentRequests; id++ {
		if _, err := fmt.Fprintf(inputWriter, `{"jsonrpc":"2.0","id":%d,"method":"tools/list"}`+"\n", id); err != nil {
			t.Fatalf("write MCP request: %v", err)
		}
	}
	for range mcpMaxConcurrentRequests {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("MCP worker did not start")
		}
	}
	if _, err := io.WriteString(inputWriter, `{"jsonrpc":"2.0","id":"queued","method":"tools/list"}`+"\n"); err != nil {
		t.Fatalf("write queued MCP request: %v", err)
	}
	if _, err := io.WriteString(inputWriter, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"queued"}}`+"\n"); err != nil {
		t.Fatalf("write queued MCP cancellation: %v", err)
	}
	if _, err := io.WriteString(inputWriter, `{"jsonrpc":"2.0","id":"queued","method":"tools/list"}`+"\n"); err != nil {
		t.Fatalf("write queued MCP duplicate: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for !strings.Contains(output.String(), "request id is already in progress") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !strings.Contains(output.String(), "request id is already in progress") {
		t.Fatalf("queued cancellation barrier response is missing: %q", output.String())
	}
	close(release)
	if err := inputWriter.Close(); err != nil {
		t.Fatalf("close MCP input: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("serveMCPWithHandler returned error: %v", err)
	}
	select {
	case id := <-started:
		if id == `"queued"` {
			t.Fatal("cancelled queued request reached the handler")
		}
	default:
	}
	if strings.Contains(output.String(), `"late":true`) {
		t.Fatalf("cancelled queued request reached the response path: %q", output.String())
	}
}

func TestServeMCPRejectsDuplicateInFlightIDAndAllowsReuse(t *testing.T) {
	inputReader, inputWriter := io.Pipe()
	defer inputReader.Close()
	started := make(chan struct{}, 2)
	release := make(chan struct{}, 2)
	handler := func(_ context.Context, message mcpMessage) mcpResponse {
		started <- struct{}{}
		<-release
		return mcpResponse{JSONRPC: "2.0", ID: message.ID, Result: map[string]bool{"ok": true}}
	}
	var output lockedMCPBuffer
	done := make(chan error, 1)
	go func() { done <- serveMCPWithHandler(t.Context(), inputReader, &output, handler) }()
	request := `{"jsonrpc":"2.0","id":"same","method":"tools/list"}` + "\n"
	if _, err := io.WriteString(inputWriter, request); err != nil {
		t.Fatalf("write first MCP request: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first MCP request did not start")
	}
	if _, err := io.WriteString(inputWriter, request); err != nil {
		t.Fatalf("write duplicate MCP request: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for !strings.Contains(output.String(), "request id is already in progress") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !strings.Contains(output.String(), "request id is already in progress") {
		t.Fatalf("duplicate MCP response is missing: %q", output.String())
	}
	release <- struct{}{}
	deadline = time.Now().Add(time.Second)
	for strings.Count(output.String(), `"id":"same"`) < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if _, err := io.WriteString(inputWriter, request); err != nil {
		t.Fatalf("reuse MCP request id: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("completed MCP request id was not reusable")
	}
	release <- struct{}{}
	if err := inputWriter.Close(); err != nil {
		t.Fatalf("close MCP input: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("serveMCPWithHandler returned error: %v", err)
	}
	if got := strings.Count(output.String(), `"id":"same"`); got != 3 {
		t.Fatalf("responses for reused ID = %d, want duplicate rejection plus two successes: %q", got, output.String())
	}
}

func TestServeMCPRunsIndependentMutatingToolsConcurrently(t *testing.T) {
	release := make(chan struct{}, 2)
	started := make(chan struct{}, 2)
	var active atomic.Int32
	var maximum atomic.Int32
	handler := func(_ context.Context, message mcpMessage) mcpResponse {
		current := active.Add(1)
		for previous := maximum.Load(); current > previous && !maximum.CompareAndSwap(previous, current); previous = maximum.Load() {
		}
		started <- struct{}{}
		<-release
		active.Add(-1)
		return mcpResponse{JSONRPC: "2.0", ID: message.ID, Result: map[string]bool{"ok": true}}
	}
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"rstream_auth_poll","arguments":{"id":"a"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"rstream_runtime_prepare","arguments":{}}}`,
		"",
	}, "\n")
	var output bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- serveMCPWithHandler(t.Context(), strings.NewReader(input), &output, handler) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first mutating MCP request did not start")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("second mutating MCP request did not run concurrently")
	}
	release <- struct{}{}
	release <- struct{}{}
	if err := <-done; err != nil {
		t.Fatalf("serveMCPWithHandler returned error: %v", err)
	}
	if got := maximum.Load(); got != 2 {
		t.Fatalf("maximum concurrent mutating requests = %d, want 2", got)
	}
}

func TestServeMCPIgnoresInitializeCancellation(t *testing.T) {
	inputReader, inputWriter := io.Pipe()
	defer inputReader.Close()
	started := make(chan struct{})
	release := make(chan struct{})
	cancelled := make(chan struct{}, 1)
	handler := func(ctx context.Context, message mcpMessage) mcpResponse {
		close(started)
		select {
		case <-ctx.Done():
			cancelled <- struct{}{}
		case <-release:
		}
		return mcpResponse{JSONRPC: "2.0", ID: message.ID, Result: map[string]bool{"ok": true}}
	}
	var output bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- serveMCPWithHandler(t.Context(), inputReader, &output, handler) }()
	if _, err := io.WriteString(inputWriter, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`+"\n"); err != nil {
		t.Fatalf("write initialize request: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("initialize request did not start")
	}
	if _, err := io.WriteString(inputWriter, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1,"reason":"test"}}`+"\n"); err != nil {
		t.Fatalf("write initialize cancellation: %v", err)
	}
	select {
	case <-cancelled:
		t.Fatal("initialize request was cancelled")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := inputWriter.Close(); err != nil {
		t.Fatalf("close MCP input: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("serveMCPWithHandler returned error: %v", err)
	}
	if !strings.Contains(output.String(), `"id":1`) {
		t.Fatalf("initialize response is missing: %q", output.String())
	}
}

func TestServeMCPIgnoresMalformedCancellationID(t *testing.T) {
	inputReader, inputWriter := io.Pipe()
	defer inputReader.Close()
	started := make(chan struct{})
	release := make(chan struct{})
	cancelled := make(chan struct{}, 1)
	handler := func(ctx context.Context, message mcpMessage) mcpResponse {
		close(started)
		select {
		case <-ctx.Done():
			cancelled <- struct{}{}
		case <-release:
		}
		return mcpResponse{JSONRPC: "2.0", ID: message.ID, Result: map[string]bool{"ok": true}}
	}
	var output bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- serveMCPWithHandler(t.Context(), inputReader, &output, handler) }()
	if _, err := io.WriteString(inputWriter, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`+"\n"); err != nil {
		t.Fatalf("write MCP request: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("MCP request did not start")
	}
	if _, err := io.WriteString(inputWriter, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":true}}`+"\n"); err != nil {
		t.Fatalf("write malformed MCP cancellation: %v", err)
	}
	select {
	case <-cancelled:
		t.Fatal("malformed cancellation ID cancelled the request")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := inputWriter.Close(); err != nil {
		t.Fatalf("close MCP input: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("serveMCPWithHandler returned error: %v", err)
	}
	if !strings.Contains(output.String(), `"id":1`) || !strings.Contains(output.String(), `"ok":true`) {
		t.Fatalf("request response is missing after malformed cancellation: %q", output.String())
	}
}

func TestMCPRuntimeContextUpdatesAreAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	initial := config.Config{Environments: []config.Environment{{
		APIURL: "https://api.example.com",
		Auth: &config.Auth{Token: &config.Token{Storage: &config.TokenStorage{
			Kind:  config.TokenStorageInline,
			Value: "login-token",
		}}},
	}}}
	if err := config.WriteAtomic(path, initial); err != nil {
		t.Fatalf("WriteAtomic returned error: %v", err)
	}
	projects := []controlplane.Project{
		{ID: "one", Name: "One", Endpoint: "one00001", Domain: "one.example.com", EnginePort: 443},
		{ID: "two", Name: "Two", Endpoint: "two00002", Domain: "two.example.com", EnginePort: 443},
	}
	errors := make(chan error, len(projects))
	var start sync.WaitGroup
	start.Add(1)
	var workers sync.WaitGroup
	for _, project := range projects {
		project := project
		workers.Add(1)
		go func() {
			defer workers.Done()
			start.Wait()
			_, _, err := mcpUpsertRuntimeContext(path, initial, "https://api.example.com", project, project.Name, false)
			errors <- err
		}()
	}
	start.Done()
	workers.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("mcpUpsertRuntimeContext returned error: %v", err)
		}
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	for _, project := range projects {
		contextValue, _, findErr := loaded.FindContextByName(project.Name)
		if findErr != nil || contextValue == nil || contextValue.ProjectEndpoint != project.Endpoint {
			t.Fatalf("context %s was lost: value=%#v error=%v", project.Name, contextValue, findErr)
		}
	}
}

func TestMCPRuntimeContextRepairsStaleNoChangeSnapshot(t *testing.T) {
	apiURL := "https://api.example.com"
	project := controlplane.Project{ID: "one", Name: "One", Endpoint: "one00001", Domain: "one.example.com", EnginePort: 443}
	stale := config.Config{
		Environments: []config.Environment{{
			APIURL: apiURL,
			Auth: &config.Auth{Token: &config.Token{Storage: &config.TokenStorage{
				Kind:  config.TokenStorageInline,
				Value: "login-token",
			}}},
		}},
		Contexts: []config.Context{{Name: project.Name, APIURL: apiURL, Engine: project.EngineAddress(), ProjectEndpoint: project.Endpoint, TURNDomain: project.Domain}},
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	latest := config.Config{Environments: stale.Environments}
	if err := config.WriteAtomic(path, latest); err != nil {
		t.Fatalf("WriteAtomic returned error: %v", err)
	}
	contextValue, changed, err := mcpUpsertRuntimeContext(path, stale, apiURL, project, project.Name, false)
	if err != nil {
		t.Fatalf("mcpUpsertRuntimeContext returned error: %v", err)
	}
	if !changed || contextValue.ProjectEndpoint != project.Endpoint {
		t.Fatalf("stale snapshot repair changed=%v context=%#v", changed, contextValue)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	stored, _, err := loaded.FindContextByName(project.Name)
	if err != nil || stored == nil || stored.ProjectEndpoint != project.Endpoint {
		t.Fatalf("repaired context value=%#v error=%v", stored, err)
	}
}

func TestServeMCPOutputFailureCancelsInFlightRequests(t *testing.T) {
	writeErr := errors.New("output closed")
	started := make(chan json.RawMessage, 2)
	releaseFirst := make(chan struct{})
	cancelledSecond := make(chan struct{})
	handler := func(ctx context.Context, message mcpMessage) mcpResponse {
		started <- message.ID
		if string(message.ID) == "1" {
			<-releaseFirst
		} else {
			<-ctx.Done()
			close(cancelledSecond)
		}
		return mcpResponse{JSONRPC: "2.0", ID: message.ID, Result: map[string]bool{"ok": true}}
	}
	input := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n" + `{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"
	done := make(chan error, 1)
	go func() {
		done <- serveMCPWithHandler(t.Context(), strings.NewReader(input), failingMCPWriter{err: writeErr}, handler)
	}()
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("MCP request did not start")
		}
	}
	close(releaseFirst)
	select {
	case <-cancelledSecond:
	case <-time.After(time.Second):
		t.Fatal("output failure did not cancel the in-flight request")
	}
	if err := <-done; !errors.Is(err, writeErr) {
		t.Fatalf("serveMCPWithHandler error = %v, want %v", err, writeErr)
	}
}

func TestMCPReadRejectsInvalidContentLength(t *testing.T) {
	oversized := fmt.Sprintf("Content-Length: %d\r\n\r\n{}", mcpMaxMessageBytes+1)
	if _, err := readMCPMessage(bufio.NewReader(strings.NewReader(oversized))); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized Content-Length error = %v", err)
	}
	negative := "Content-Length: -1\r\n\r\n{}"
	if _, err := readMCPMessage(bufio.NewReader(strings.NewReader(negative))); err == nil || !strings.Contains(err.Error(), "negative") {
		t.Fatalf("negative Content-Length error = %v", err)
	}
}

func TestMCPToolsListContainsAgentNativeTools(t *testing.T) {
	response := handleMCPMessage(t.Context(), mcpMessage{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/list"})
	if response.Error != nil {
		t.Fatalf("unexpected error: %#v", response.Error)
	}
	payload, err := json.Marshal(response.Result)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	for _, want := range []string{"rstream_auth_poll", "rstream_auth_start", "rstream_context_list", "rstream_context_get", "rstream_project_creation_options", "rstream_project_create", "rstream_project_update", "rstream_project_delete", "rstream_project_events_list", "rstream_project_list", "rstream_project_logs", "rstream_project_usage", "rstream_project_plan_get", "rstream_project_turn_usage", "rstream_project_turn_credentials_create", "rstream_project_webhooks_list", "rstream_project_webhook_deliveries_list", "rstream_project_webhook_delivery_get", "rstream_project_domains_list", "rstream_project_domain_create", "rstream_project_domain_get", "rstream_project_domain_delete", "rstream_project_domain_verify", "rstream_project_domain_connect", "rstream_project_tcp_addresses_list", "rstream_project_tcp_address_reserve", "rstream_project_tcp_address_release", "rstream_project_settings_get", "rstream_project_settings_patch", "rstream_project_settings_reset", "rstream_local_tunnel_expose", "rstream_local_tunnel_list", "rstream_local_tunnel_stop", "rstream_remote_expose", "rstream_remote_expose_stop", "rstream_remote_mcp_discover", "rstream_remote_mcp_tools", "rstream_remote_mcp_call", "rstream_runtime_prepare", "rstream_runtime_status", "rstream_token_create", "rstream_workspace_list", "rstream_workspace_members_list", "rstream_webtty_list", "rstream_webtty_servers_list", "rstream_webtty_server_get", "rstream_webtty_server_create", "rstream_webtty_server_update", "rstream_webtty_server_delete", "rstream_webtty_server_enrollment_get", "rstream_webtty_exec", "rstream_webtty_sessions_list", "rstream_webtty_session_get", "rstream_webtty_session_events", "rstream_webtty_session_export", "rstream_webtty_session_participants", "rstream_webtty_control_requests_list", "rstream_webtty_control_request_create", "rstream_webtty_control_request_resolve", "rstream_webtty_fs_list", "rstream_webtty_fs_read", "rstream_webtty_fs_download", "rstream_webtty_fs_write", "rstream_webtty_fs_mkdir", "rstream_webtty_fs_delete"} {
		if !strings.Contains(string(payload), want) {
			t.Fatalf("tools/list missing %q: %s", want, string(payload))
		}
	}
	if !strings.Contains(string(payload), `"title":"Prepare rstream runtime"`) || !strings.Contains(string(payload), `"annotations"`) || !strings.Contains(string(payload), `"readOnlyHint"`) {
		t.Fatalf("tools/list does not expose MCP title and annotations: %s", string(payload))
	}
	type listedTool struct {
		Annotations  map[string]any `json:"annotations"`
		Meta         map[string]any `json:"_meta"`
		Name         string         `json:"name"`
		OutputSchema map[string]any `json:"outputSchema"`
	}
	var listed struct {
		Tools []listedTool `json:"tools"`
	}
	if err := json.Unmarshal(payload, &listed); err != nil {
		t.Fatalf("tools/list JSON is invalid: %v", err)
	}
	if len(listed.Tools) != len(mcpToolBehaviors) {
		t.Fatalf("tools/list returned %d tools for %d declared behaviors", len(listed.Tools), len(mcpToolBehaviors))
	}
	toolsByName := map[string]listedTool{}
	for _, tool := range listed.Tools {
		behavior, ok := mcpToolBehaviors[tool.Name]
		if !ok {
			t.Fatalf("tool %q has no declared behavior", tool.Name)
		}
		for key, want := range map[string]bool{"readOnlyHint": behavior.ReadOnly, "destructiveHint": behavior.Destructive, "idempotentHint": behavior.Idempotent, "openWorldHint": behavior.OpenWorld} {
			if got := tool.Annotations[key]; got != want {
				t.Fatalf("%s %s = %#v, want %v", tool.Name, key, got, want)
			}
		}
		if tool.Annotations["title"] != behavior.Title {
			t.Fatalf("%s annotation title = %#v, want %q", tool.Name, tool.Annotations["title"], behavior.Title)
		}
		if tool.OutputSchema["type"] != "object" {
			t.Fatalf("%s output schema must have an object root: %#v", tool.Name, tool.OutputSchema)
		}
		for _, key := range []string{"openai/toolInvocation/invoking", "openai/toolInvocation/invoked"} {
			status, ok := tool.Meta[key].(string)
			if !ok || strings.TrimSpace(status) == "" || len(status) > 64 {
				t.Fatalf("%s %s must be a non-empty status of at most 64 characters: %#v", tool.Name, key, tool.Meta[key])
			}
		}
		toolsByName[tool.Name] = tool
	}
	if got := toolsByName["rstream_webtty_control_request_create"].Annotations["destructiveHint"]; got != false {
		t.Fatalf("control request creation must not be advertised as destructive: %#v", got)
	}
	for _, name := range []string{"rstream_auth_poll", "rstream_project_update", "rstream_webtty_server_update", "rstream_webtty_exec", "rstream_webtty_fs_download"} {
		if got := toolsByName[name].Annotations["destructiveHint"]; got != true {
			t.Fatalf("%s destructiveHint = %#v, want true", name, got)
		}
	}
	if !strings.Contains(string(payload), `"outputSchema"`) || !strings.Contains(string(payload), `"login_url"`) || !strings.Contains(string(payload), `"suggested_next_tool"`) {
		t.Fatalf("tools/list does not expose key output schemas: %s", string(payload))
	}
	for name, key := range map[string]string{"rstream_workspace_list": "workspaces", "rstream_project_list": "projects", "rstream_remote_mcp_discover": "endpoints", "rstream_webtty_list": "servers", "rstream_webtty_fs_list": "entries"} {
		properties, ok := toolsByName[name].OutputSchema["properties"].(map[string]any)
		if !ok || properties[key] == nil {
			t.Fatalf("%s output schema does not declare semantic key %q: %#v", name, key, toolsByName[name].OutputSchema)
		}
	}
	if !strings.Contains(string(payload), "read-only project token") || !strings.Contains(string(payload), "tunnels.resources.read-only requires list") {
		t.Fatalf("tools/list does not document token resource examples: %s", string(payload))
	}
	for _, want := range []string{"\"exec_path\"", "\"fs_path\""} {
		if !strings.Contains(string(payload), want) {
			t.Fatalf("tools/list missing WebTTY path argument %q: %s", want, string(payload))
		}
	}
}

func TestMCPAgentGuidanceIncludesEngineAuthBoundaries(t *testing.T) {
	guidance := mcpAgentGuidance()
	engineAuth, ok := guidance["engine_api_auth"].(map[string]any)
	if !ok {
		t.Fatalf("engine_api_auth guidance missing: %#v", guidance)
	}
	payload, err := json.Marshal(engineAuth)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	for _, want := range []string{"/api/clients", "/api/tunnels", "/api/sse", "/api/websocket", "rstream.token", "short-lived auth or app token", "explicit read-only watch permissions", "list-only tunnel resources"} {
		if !strings.Contains(string(payload), want) {
			t.Fatalf("engine auth guidance missing %q: %s", want, string(payload))
		}
	}
}

func TestMCPContextGetUsesSelectedEnvironmentContext(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte(`version: 1
defaults:
  context:
    name: prod
contexts:
  - name: prod
    apiUrl: https://rstream.io
    projectEndpoint: prod-endpoint
    engine: prod.c.rstream.io:443
  - name: tests
    apiUrl: http://localhost:3000
    projectEndpoint: test-endpoint
    engine: test.c.localhost.rstream.io:443
`), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	t.Setenv("RSTREAM_CONFIG", configPath)
	t.Setenv("RSTREAM_CONTEXT", "tests")
	contextResult, err := mcpContextGet(map[string]json.RawMessage{})
	if err != nil {
		t.Fatalf("mcpContextGet returned error: %v", err)
	}
	contextText := mcpResultText(t, contextResult)
	if !strings.Contains(contextText, `"Name": "tests"`) || !strings.Contains(contextText, `"ProjectEndpoint": "test-endpoint"`) {
		t.Fatalf("unexpected selected context: %s", contextText)
	}
	listResult, err := mcpContextList(map[string]json.RawMessage{})
	if err != nil {
		t.Fatalf("mcpContextList returned error: %v", err)
	}
	if listText := mcpResultText(t, listResult); !strings.Contains(listText, `"selected": "tests"`) {
		t.Fatalf("context list did not expose selected context: %s", listText)
	}
}

func TestMCPContextGetWithoutDefaultSuggestsRuntimePrepare(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte(`version: 1
environments:
  - apiUrl: https://rstream.io
    auth:
      token:
        storage:
          kind: inline
          value: login-token
`), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	t.Setenv("RSTREAM_CONFIG", configPath)
	contextResult, err := mcpContextGet(map[string]json.RawMessage{})
	if err != nil {
		t.Fatalf("mcpContextGet returned error: %v", err)
	}
	contextText := mcpResultText(t, contextResult)
	if !strings.Contains(contextText, `"needs_context": true`) || !strings.Contains(contextText, `"suggested_next_tool": "rstream_runtime_prepare"`) {
		t.Fatalf("unexpected no-context result: %s", contextText)
	}
}

func TestMCPServeFlagsApplyEnvironmentOverrides(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("api-url", "", "")
	cmd.Flags().String("config", "", "")
	cmd.Flags().String("context", "", "")
	if err := cmd.Flags().Set("api-url", "http://localhost:3000"); err != nil {
		t.Fatalf("Set api-url returned error: %v", err)
	}
	if err := cmd.Flags().Set("config", "/tmp/rstream-test-config.yaml"); err != nil {
		t.Fatalf("Set config returned error: %v", err)
	}
	if err := cmd.Flags().Set("context", "tests"); err != nil {
		t.Fatalf("Set context returned error: %v", err)
	}
	t.Setenv("RSTREAM_API_URL", "https://rstream.io")
	t.Setenv("RSTREAM_CONFIG", "/tmp/original.yaml")
	t.Setenv("RSTREAM_CONTEXT", "prod")
	restore := applyMCPServeFlagEnvironment(cmd)
	if got := os.Getenv("RSTREAM_API_URL"); got != "http://localhost:3000" {
		t.Fatalf("RSTREAM_API_URL = %q", got)
	}
	if got := os.Getenv("RSTREAM_CONFIG"); got != "/tmp/rstream-test-config.yaml" {
		t.Fatalf("RSTREAM_CONFIG = %q", got)
	}
	if got := os.Getenv("RSTREAM_CONTEXT"); got != "tests" {
		t.Fatalf("RSTREAM_CONTEXT = %q", got)
	}
	restore()
	if got := os.Getenv("RSTREAM_API_URL"); got != "https://rstream.io" {
		t.Fatalf("restored RSTREAM_API_URL = %q", got)
	}
	if got := os.Getenv("RSTREAM_CONFIG"); got != "/tmp/original.yaml" {
		t.Fatalf("restored RSTREAM_CONFIG = %q", got)
	}
	if got := os.Getenv("RSTREAM_CONTEXT"); got != "prod" {
		t.Fatalf("restored RSTREAM_CONTEXT = %q", got)
	}
}

func TestMCPTokenCreateErrorPayloadIncludesResourceHelp(t *testing.T) {
	payload := mcpTokenCreateErrorPayload(errors.New("Invalid input").Error())
	examples, ok := payload["examples"].(map[string]string)
	if payload["ok"] != false || !ok || !strings.Contains(examples["read_only_project"], `"list":true`) || !strings.Contains(payload["hint"].(string), "scopes.tunnels") {
		t.Fatalf("unexpected token create error payload: %#v", payload)
	}
}

func TestMCPTokenCreateResultPayloadIncludesMetadata(t *testing.T) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	claims := base64.RawURLEncoding.EncodeToString([]byte(`{"type":"auth","iat":10,"exp":130,"permissions":["tunnels.resources.read-only"],"resources":{"tunnels":{"projects":["project-1"]}}}`))
	payload := mcpTokenCreateResultPayload(controlplane.CreateTokenResponse{Token: header + "." + claims + "."})
	if payload["token_type"] != "auth" || payload["ttl_seconds"] != int64(120) || payload["expires_at"] != "1970-01-01T00:02:10Z" {
		t.Fatalf("unexpected token metadata: %#v", payload)
	}
	if payload["permissions"] == nil || payload["resources"] == nil {
		t.Fatalf("token metadata should include permissions and resources: %#v", payload)
	}
}

func TestMCPProjectListIgnoresExpiredDefaultContextWhenLoginTokenExists(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/projects/tunnels" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer login-token" {
			t.Fatalf("Authorization = %q", got)
		}
		_ = json.NewEncoder(w).Encode(controlplane.ListProjectsResponse{Projects: []controlplane.Project{{ID: "p1", Name: "Prod", Endpoint: "abc12345", Domain: "cluster.example.com", EnginePort: 443, Status: "active", Plan: "pro", Routing: "regional"}}})
	}))
	defer server.Close()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf(`version: 1
defaults:
  context:
    name: Prod
environments:
  - apiUrl: %s
    auth:
      token:
        storage:
          kind: inline
          value: login-token
contexts:
  - name: Prod
    apiUrl: %s
    projectEndpoint: abc12345
    engine: abc12345.cluster.example.com:443
    auth:
      token:
        storage:
          kind: inline
          value: %s
`, server.URL, server.URL, mcpTestUnsignedJWT(`{"exp":100}`))), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	t.Setenv("RSTREAM_CONFIG", configPath)
	result, err := mcpProjectList(t.Context(), map[string]json.RawMessage{})
	if err != nil {
		t.Fatalf("mcpProjectList returned error: %v", err)
	}
	if text := mcpResultText(t, result); !strings.Contains(text, `"name": "Prod"`) {
		t.Fatalf("unexpected project list: %s", text)
	}
}

func TestMCPControlPlaneRuntimeUsesExplicitContextAPIURL(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte(`version: 1
defaults:
  context:
    name: prod
environments:
  - apiUrl: https://rstream.io
    auth:
      token:
        storage:
          kind: inline
          value: prod-token
  - apiUrl: https://dev.rstream.io
    auth:
      token:
        storage:
          kind: inline
          value: staging-token
contexts:
  - name: prod
    apiUrl: https://rstream.io
    projectEndpoint: prod0001
  - name: staging
    apiUrl: https://dev.rstream.io
    projectEndpoint: dev00001
`), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	t.Setenv("RSTREAM_CONFIG", configPath)
	t.Setenv("RSTREAM_CONTEXT", "staging")
	runtime, err := resolveMCPControlPlaneRuntime(t.Context(), true)
	if err != nil {
		t.Fatalf("resolveMCPControlPlaneRuntime returned error: %v", err)
	}
	if runtime.Resolved.APIURL != "https://dev.rstream.io" || runtime.Resolved.Token != "staging-token" {
		t.Fatalf("explicit staging context resolved the wrong Control plane: %#v", runtime.Resolved)
	}
}

func TestMCPRuntimePrepareUsesLoginTokenInsteadOfShortContextToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/projects/tunnels" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer login-token" {
			t.Fatalf("Authorization = %q", got)
		}
		_ = json.NewEncoder(w).Encode(controlplane.ListProjectsResponse{Projects: []controlplane.Project{{ID: "p1", WorkspaceID: "w1", Name: "Prod", Endpoint: "abc12345", Domain: "cluster.example.com", EnginePort: 443, Status: "active", Plan: "pro", Routing: "regional", Region: "eu-west-3", TurnDomain: "relay.example.com", TurnPort: 3478, TurnRealm: "realm.example.com", TurnsPort: 5349}}})
	}))
	defer server.Close()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf(`version: 1
defaults:
  context:
    name: Prod
environments:
  - apiUrl: %s
    auth:
      token:
        storage:
          kind: inline
          value: login-token
contexts:
  - name: Prod
    apiUrl: %s
    projectEndpoint: old
    engine: old.cluster.example.com:443
    auth:
      token:
        storage:
          kind: inline
          value: %s
`, server.URL, server.URL, mcpTestUnsignedJWT(`{"exp":100}`))), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	t.Setenv("RSTREAM_CONFIG", configPath)
	result, err := mcpRuntimePrepare(t.Context(), map[string]json.RawMessage{"project": json.RawMessage(`"Prod"`)})
	if err != nil {
		t.Fatalf("mcpRuntimePrepare returned error: %v", err)
	}
	text := mcpResultText(t, result)
	if !strings.Contains(text, "no short-lived delegated token was minted") || !strings.Contains(text, `"engine": "abc12345.cluster.example.com:443"`) {
		t.Fatalf("unexpected runtime prepare payload: %s", text)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	contextValue, _, err := cfg.FindContextByName("Prod")
	if err != nil || contextValue == nil {
		t.Fatalf("FindContextByName returned %#v, %v", contextValue, err)
	}
	if contextValue.Auth != nil || contextValue.Engine != "abc12345.cluster.example.com:443" || contextValue.ProjectEndpoint != "abc12345" || contextValue.TURNDomain != "relay.example.com" || contextValue.TURNRealm != "realm.example.com" {
		t.Fatalf("unexpected prepared context: %#v", contextValue)
	}
	resolved, err := config.Resolve(config.ResolveInput{Config: cfg, EnvAPIURL: server.URL, RequireEngine: true, RequireToken: true, ResolveToken: true})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if resolved.Token != "login-token" || resolved.Engine != "abc12345.cluster.example.com:443" {
		t.Fatalf("unexpected resolved runtime: %#v", resolved)
	}
}

func TestMCPRuntimeStatusSuggestsPrepareWhenLoginTokenHasNoContext(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte(`version: 1
environments:
  - apiUrl: https://rstream.example.test
    auth:
      token:
        storage:
          kind: inline
          value: login-token
`), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	t.Setenv("RSTREAM_CONFIG", configPath)
	result, err := mcpRuntimeStatus(t.Context())
	if err != nil {
		t.Fatalf("mcpRuntimeStatus returned error: %v", err)
	}
	text := mcpResultText(t, result)
	if !strings.Contains(text, `"ready": false`) || !strings.Contains(text, `"needs_context": true`) || !strings.Contains(text, `"suggested_next_tool": "rstream_runtime_prepare"`) {
		t.Fatalf("unexpected runtime status: %s", text)
	}
}

func mcpTestUnsignedJWT(payload string) string {
	return "header." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".signature"
}

func TestMCPPersonalWorkspaceMembersPayloadIsStructured(t *testing.T) {
	payload := mcpPersonalWorkspaceMembersPayload("workspace-1")
	members, ok := payload["members"].([]any)
	if payload["ok"] != true || payload["workspace_type"] != "personal" || !ok || len(members) != 0 {
		t.Fatalf("unexpected personal workspace members payload: %#v", payload)
	}
}

func TestMCPInitializeProtocolVersion(t *testing.T) {
	response := handleMCPMessage(t.Context(), mcpMessage{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "initialize",
		Params:  json.RawMessage(`{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}`),
	})
	if response.Error != nil {
		t.Fatalf("unexpected error: %#v", response.Error)
	}
	payload, err := json.Marshal(response.Result)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if !strings.Contains(string(payload), `"protocolVersion":"`+mcpProtocolVersion+`"`) {
		t.Fatalf("initialize returned unexpected protocol version: %s", string(payload))
	}
	if !strings.Contains(string(payload), `"title":"rstream"`) || !strings.Contains(string(payload), "rstream_runtime_prepare") || !strings.Contains(string(payload), "call rstream_auth_poll with wait=true") || !strings.Contains(string(payload), "do not infer or recommend") {
		t.Fatalf("initialize missing display title or instructions: %s", string(payload))
	}
}

func TestMCPInitializeRejectsMissingRequiredParams(t *testing.T) {
	response := handleMCPMessage(t.Context(), mcpMessage{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "initialize",
		Params:  json.RawMessage(`{}`),
	})
	if response.Error == nil || response.Error.Code != -32602 || response.Error.Message != "Invalid params" {
		t.Fatalf("initialize response = %#v", response)
	}
}

func TestMCPArgumentHelpers(t *testing.T) {
	args := map[string]json.RawMessage{
		"empty": json.RawMessage(`""`),
		"name":  json.RawMessage(`" shell "`),
		"list":  json.RawMessage(`[" a ","","b"]`),
	}
	empty, err := mcpStringArg(args, "empty")
	if err != nil || empty != "" {
		t.Fatalf("mcpStringArg(empty) = %q, %v", empty, err)
	}
	value, err := mcpRequiredStringArg(args, "name")
	if err != nil || value != " shell " {
		t.Fatalf("mcpRequiredStringArg = %q, %v", value, err)
	}
	values, err := mcpRequiredStringSliceArg(args, "list")
	if err != nil || len(values) != 2 || values[0] != "a" || values[1] != "b" {
		t.Fatalf("mcpRequiredStringSliceArg = %#v, %v", values, err)
	}
	if _, err := mcpRequiredStringArg(args, "missing"); err == nil {
		t.Fatalf("expected missing string argument error")
	}
	if _, err := mcpStringArg(args, "missing"); err == nil {
		t.Fatalf("expected missing raw string argument error")
	}
	boolValue, err := mcpOptionalBoolArg(map[string]json.RawMessage{"flag": json.RawMessage(`true`)}, "flag", false)
	if err != nil || !boolValue {
		t.Fatalf("mcpOptionalBoolArg = %v, %v", boolValue, err)
	}
	intValue, err := mcpOptionalIntArg(map[string]json.RawMessage{"page": json.RawMessage(`2`)}, "page")
	if err != nil || intValue == nil || *intValue != 2 {
		t.Fatalf("mcpOptionalIntArg = %#v, %v", intValue, err)
	}
}

func TestMCPWebTTYFilterArgs(t *testing.T) {
	for _, tc := range []struct {
		Args       map[string]json.RawMessage
		WantFilter string
		WantName   string
	}{
		{Args: map[string]json.RawMessage{"filter": json.RawMessage(`"shell"`)}, WantName: "shell"},
		{Args: map[string]json.RawMessage{"filter": json.RawMessage(`"labels.site=lab"`)}, WantFilter: "labels.site=lab"},
		{Args: map[string]json.RawMessage{"name": json.RawMessage(`"shell"`)}, WantName: "shell"},
		{Args: map[string]json.RawMessage{"filter": json.RawMessage(`"labels.site=lab"`), "name": json.RawMessage(`"shell"`)}, WantFilter: "labels.site=lab", WantName: "shell"},
	} {
		gotFilter, gotName, err := mcpWebTTYFilterArgs(tc.Args)
		if err != nil {
			t.Fatalf("mcpWebTTYFilterArgs returned error: %v", err)
		}
		if gotFilter != tc.WantFilter || gotName != tc.WantName {
			t.Fatalf("mcpWebTTYFilterArgs(%#v) = %q, %q; want %q, %q", tc.Args, gotFilter, gotName, tc.WantFilter, tc.WantName)
		}
	}
}

func TestMCPWebTTYExecClientConfigKeepsPlainWhenNoE2ESignal(t *testing.T) {
	clearRstreamTestEnv(t)
	args := map[string]json.RawMessage{
		"url": json.RawMessage(`"ws://127.0.0.1:6002"`),
		"env": json.RawMessage(`["A=B"]`),
	}
	workdir := "/tmp"
	args["workdir"] = json.RawMessage(`"` + workdir + `"`)
	cfg, err := mcpWebTTYExecClientConfig(t.Context(), args, []string{"whoami"})
	if err != nil {
		t.Fatalf("mcpWebTTYExecClientConfig() error = %v", err)
	}
	if cfg.PayloadCrypto != nil {
		t.Fatalf("plain MCP WebTTY exec should not enable payload crypto: %#v", cfg.PayloadCrypto)
	}
	if cfg.URL != "ws://127.0.0.1:6002" || len(cfg.CmdArgs) != 1 || cfg.CmdArgs[0] != "whoami" || cfg.Workdir == nil || *cfg.Workdir != workdir {
		t.Fatalf("unexpected MCP WebTTY exec config: %#v", cfg)
	}
}

func TestMCPWebTTYExecClientConfigUsesKnownServerKey(t *testing.T) {
	clearRstreamTestEnv(t)
	identity, err := webtty.GenerateE2EIdentity()
	if err != nil {
		t.Fatalf("GenerateE2EIdentity() error = %v", err)
	}
	t.Setenv(webTTYKnownServerKeyEnv, webtty.EncodeE2EKeyMaterial(identity.PublicKey))
	cfg, err := mcpWebTTYExecClientConfig(t.Context(), map[string]json.RawMessage{
		"url": json.RawMessage(`"ws://127.0.0.1:6002"`),
	}, []string{"whoami"})
	if err != nil {
		t.Fatalf("mcpWebTTYExecClientConfig() error = %v", err)
	}
	if cfg.PayloadCrypto == nil || cfg.PayloadCrypto.SessionKeyGrant == nil || len(cfg.PayloadCrypto.SessionKeyGrant.KeyEnvelopes) != 1 {
		t.Fatalf("expected MCP WebTTY exec E2E payload crypto from known server key, got %#v", cfg.PayloadCrypto)
	}
}

func TestMCPWebTTYExecClientConfigUsesKnownServerName(t *testing.T) {
	clearRstreamTestEnv(t)
	serverIdentity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path, err := webtty.DefaultKnownServerKeysPath()
	if err != nil {
		t.Fatalf("DefaultKnownServerKeysPath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	doc := webtty.KnownServerKeysFile{
		Version:     webtty.E2EIdentityFileVersion,
		CryptoSuite: webtty.E2EKeyFileCryptoSuite,
		KnownServers: []webtty.KnownServerKeyEntry{{
			Name:             "prod-shell",
			KeyID:            webtty.EncodeE2EKeyMaterial(serverIdentity.Encryption.KeyID),
			PublicKey:        webtty.EncodeE2EKeyMaterial(serverIdentity.Encryption.PublicKey),
			SigningKeyID:     webtty.EncodeE2EKeyMaterial(serverIdentity.Signing.KeyID),
			SigningPublicKey: webtty.EncodeE2EKeyMaterial(serverIdentity.Signing.PublicKey),
			ClientIdentity:   "operator-laptop",
		}},
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := writeTestWebTTYEndpointIdentity(t, "operator-laptop"); err != nil {
		t.Fatalf("writeTestWebTTYEndpointIdentity() error = %v", err)
	}
	cfg, err := mcpWebTTYExecClientConfig(t.Context(), map[string]json.RawMessage{
		"url":          json.RawMessage(`"ws://127.0.0.1:6002"`),
		"known_server": json.RawMessage(`"prod-shell"`),
	}, []string{"whoami"})
	if err != nil {
		t.Fatalf("mcpWebTTYExecClientConfig() error = %v", err)
	}
	if cfg.PayloadCrypto == nil || cfg.ExpectedServerIdentity == nil || cfg.EndpointIdentity == nil {
		t.Fatalf("expected MCP WebTTY exec crypto and endpoint identity from known server, got %#v", cfg)
	}
	if !bytes.Equal(cfg.ExpectedServerIdentity.SigningKeyID, serverIdentity.Signing.KeyID) {
		t.Fatalf("known_server selected wrong server identity")
	}
}

func TestMCPJSONResourceLinkResultIncludesStructuredContentAndLink(t *testing.T) {
	result, err := mcpJSONResourceLinkResult(map[string]any{"url": "https://local-tunnel.example.com"}, false, "https://local-tunnel.example.com", "local tunnel", "Public local tunnel", "text/html")
	if err != nil {
		t.Fatalf("mcpJSONResourceLinkResult returned error: %v", err)
	}
	content, ok := result["content"].([]map[string]any)
	if !ok || len(content) != 2 {
		t.Fatalf("unexpected content: %#v", result["content"])
	}
	if content[1]["type"] != "resource_link" || content[1]["uri"] != "https://local-tunnel.example.com" {
		t.Fatalf("unexpected resource link: %#v", content[1])
	}
	if structured, ok := result["structuredContent"].(map[string]any); !ok || structured["url"] != "https://local-tunnel.example.com" {
		t.Fatalf("unexpected structured content: %#v", result["structuredContent"])
	}
}

func TestMCPJSONResultUsesStructuredObjectForStructs(t *testing.T) {
	type response struct {
		Ready bool   `json:"ready"`
		Name  string `json:"name"`
	}
	result, err := mcpJSONResult(response{Ready: true, Name: "prod"}, false)
	if err != nil {
		t.Fatalf("mcpJSONResult returned error: %v", err)
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("structured content is not an object: %#v", result["structuredContent"])
	}
	if structured["result"] != nil {
		t.Fatalf("structured content should not wrap object results: %#v", structured)
	}
	if structured["ready"] != true || structured["name"] != "prod" {
		t.Fatalf("unexpected structured content: %#v", structured)
	}
}

func TestMCPJSONResultRejectsNonObjectStructuredContent(t *testing.T) {
	if _, err := mcpJSONResult([]string{"one", "two"}, false); err == nil || !strings.Contains(err.Error(), "JSON object") {
		t.Fatalf("mcpJSONResult array error = %v", err)
	}
	if _, err := mcpJSONResult("value", false); err == nil || !strings.Contains(err.Error(), "JSON object") {
		t.Fatalf("mcpJSONResult scalar error = %v", err)
	}
}

func TestMCPWebTTYExecResultPreservesCapturedOutput(t *testing.T) {
	base := webTTYClientResult{URL: "rstrm://shell", Command: []string{"sh", "-lc", "build"}, ExitCode: 0, Stdout: "partial stdout", Stderr: "partial stderr", DurationMS: 42}
	nonzero := base
	nonzero.ExitCode = 7
	tests := []struct {
		name      string
		result    webTTYClientResult
		runErr    error
		errorKind string
		wantError bool
	}{
		{name: "success", result: base},
		{name: "nonzero exit", result: nonzero, wantError: true},
		{name: "session failure", result: base, runErr: errors.New("connection closed"), errorKind: "session", wantError: true},
		{name: "cancelled", result: base, runErr: context.Canceled, errorKind: "cancelled", wantError: true},
		{name: "timeout", result: base, runErr: context.DeadlineExceeded, errorKind: "timeout", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := mcpWebTTYExecResult(&test.result, test.runErr)
			if err != nil {
				t.Fatalf("mcpWebTTYExecResult returned error: %v", err)
			}
			if result["isError"] != test.wantError {
				t.Fatalf("isError = %#v, want %v", result["isError"], test.wantError)
			}
			structured, ok := result["structuredContent"].(map[string]any)
			if !ok {
				t.Fatalf("structured content is not an object: %#v", result["structuredContent"])
			}
			command, commandOK := structured["command"].([]any)
			if structured["surface"] != "cli" || structured["url"] != base.URL || !commandOK || len(command) != len(base.Command) || structured["stdout"] != base.Stdout || structured["stderr"] != base.Stderr || structured["duration_ms"] != float64(base.DurationMS) || structured["truncated"] != false {
				t.Fatalf("captured output was not preserved: %#v", structured)
			}
			if test.runErr == nil {
				if structured["error"] != nil || structured["error_kind"] != nil {
					t.Fatalf("nonzero exit should not be reported as a session error: %#v", structured)
				}
				return
			}
			if structured["error"] != "WebTTY command execution failed." || structured["error_kind"] != test.errorKind {
				t.Fatalf("unexpected execution error payload: %#v", structured)
			}
		})
	}
}

func TestMCPWebTTYExecResultRejectsMissingCapture(t *testing.T) {
	runErr := errors.New("capture failed")
	if _, err := mcpWebTTYExecResult(nil, runErr); !errors.Is(err, runErr) {
		t.Fatalf("mcpWebTTYExecResult error = %v, want %v", err, runErr)
	}
}

func TestMCPContentReaderSupportsTextAndBase64(t *testing.T) {
	reader, err := mcpContentReader(map[string]json.RawMessage{}, "hello")
	if err != nil {
		t.Fatalf("mcpContentReader(text) returned error: %v", err)
	}
	data := bytes.Buffer{}
	if _, err := data.ReadFrom(reader); err != nil {
		t.Fatalf("failed to read text content: %v", err)
	}
	if data.String() != "hello" {
		t.Fatalf("text content = %q", data.String())
	}
	reader, err = mcpContentReader(map[string]json.RawMessage{"encoding": json.RawMessage(`"base64"`)}, "aGVsbG8=")
	if err != nil {
		t.Fatalf("mcpContentReader(base64) returned error: %v", err)
	}
	data.Reset()
	if _, err := data.ReadFrom(reader); err != nil {
		t.Fatalf("failed to read base64 content: %v", err)
	}
	if data.String() != "hello" {
		t.Fatalf("base64 content = %q", data.String())
	}
	if _, err := mcpContentReader(map[string]json.RawMessage{"encoding": json.RawMessage(`"gzip"`)}, "hello"); err == nil {
		t.Fatalf("expected invalid encoding error")
	}
}

func TestMCPCreateProjectArgsRequiresExplicitBillingInputs(t *testing.T) {
	args := map[string]json.RawMessage{"workspace_id": json.RawMessage(`"ws1"`), "name": json.RawMessage(`"Codex Demo"`), "provider": json.RawMessage(`"aws"`), "region": json.RawMessage(`"eu-west-3"`), "plan": json.RawMessage(`"basic"`), "creation_fingerprint": json.RawMessage(`"fingerprint"`)}
	workspaceID, request, err := mcpCreateProjectArgs(args)
	if err != nil {
		t.Fatalf("mcpCreateProjectArgs returned error: %v", err)
	}
	if workspaceID != "ws1" || request.Name != "Codex Demo" || request.Routing != "regional" || request.Provider != "aws" || request.Region != "eu-west-3" || request.Plan != "basic" || request.CreationFingerprint != "fingerprint" {
		t.Fatalf("unexpected request: workspace=%q request=%#v", workspaceID, request)
	}
	if !strings.HasPrefix(request.IdempotencyKey, "mcp:") {
		t.Fatalf("missing generated idempotency key: %q", request.IdempotencyKey)
	}
	globalArgs := map[string]json.RawMessage{"workspace_id": json.RawMessage(`"ws1"`), "name": json.RawMessage(`"Global"`), "routing": json.RawMessage(`"global"`), "provider": json.RawMessage(`"aws"`), "plan": json.RawMessage(`"pro"`), "creation_fingerprint": json.RawMessage(`"fingerprint"`)}
	_, globalRequest, err := mcpCreateProjectArgs(globalArgs)
	if err != nil {
		t.Fatalf("mcpCreateProjectArgs(global) returned error: %v", err)
	}
	if globalRequest.Routing != "global" || globalRequest.Provider != "aws" || globalRequest.Region != "" {
		t.Fatalf("unexpected global request: %#v", globalRequest)
	}
	enterpriseArgs := map[string]json.RawMessage{"workspace_id": json.RawMessage(`"ws1"`), "name": json.RawMessage(`"Enterprise"`), "plan": json.RawMessage(`"enterprise"`), "creation_fingerprint": json.RawMessage(`"fingerprint"`)}
	_, enterpriseRequest, err := mcpCreateProjectArgs(enterpriseArgs)
	if err != nil {
		t.Fatalf("mcpCreateProjectArgs(enterprise) returned error: %v", err)
	}
	if enterpriseRequest.Provider != "" || enterpriseRequest.Region != "" {
		t.Fatalf("unexpected enterprise request: %#v", enterpriseRequest)
	}
	enterpriseArgs["provider"] = json.RawMessage(`"aws"`)
	if _, _, err := mcpCreateProjectArgs(enterpriseArgs); err == nil {
		t.Fatal("expected enterprise provider without region error")
	}
	delete(globalArgs, "routing")
	delete(globalArgs, "provider")
	if _, _, err := mcpCreateProjectArgs(globalArgs); err == nil {
		t.Fatal("expected missing provider error")
	}
	delete(args, "creation_fingerprint")
	if _, _, err := mcpCreateProjectArgs(args); err == nil {
		t.Fatalf("expected missing creation fingerprint error")
	}
}

func TestMCPControlPlaneArgs(t *testing.T) {
	args := map[string]json.RawMessage{"timeline": json.RawMessage(`"1h"`), "event_type": json.RawMessage(`"connection.closed"`), "page_size": json.RawMessage(`5`)}
	logs, err := mcpProjectLogsParams(args)
	if err != nil {
		t.Fatalf("mcpProjectLogsParams returned error: %v", err)
	}
	if logs.Timeline != "1h" || logs.EventType != "connection.closed" || logs.PageSize == nil || *logs.PageSize != 5 {
		t.Fatalf("unexpected logs params: %#v", logs)
	}
	domains, err := mcpListProjectDomainsParams(map[string]json.RawMessage{"q": json.RawMessage(`"codex"`), "page_size": json.RawMessage(`10`)})
	if err != nil {
		t.Fatalf("mcpListProjectDomainsParams returned error: %v", err)
	}
	if domains.Query != "codex" || domains.PageSize == nil || *domains.PageSize != 10 {
		t.Fatalf("unexpected domains params: %#v", domains)
	}
	settings, err := mcpProjectSettingsPatchArg(map[string]json.RawMessage{"settings": json.RawMessage(`{"publicAccessPolicy":"forbidden"}`)})
	if err != nil {
		t.Fatalf("mcpProjectSettingsPatchArg(object) returned error: %v", err)
	}
	if settings["publicAccessPolicy"] != "forbidden" {
		t.Fatalf("unexpected settings: %#v", settings)
	}
	if _, err := mcpProjectSettingsPatchArg(map[string]json.RawMessage{}); err == nil || !strings.Contains(err.Error(), "settings") {
		t.Fatalf("missing settings error = %v", err)
	}
	members, err := mcpWorkspaceMembersParams(map[string]json.RawMessage{"q": json.RawMessage(`"admin"`), "page_size": json.RawMessage(`10`)})
	if err != nil {
		t.Fatalf("mcpWorkspaceMembersParams returned error: %v", err)
	}
	if members.Query != "admin" || members.PageSize == nil || *members.PageSize != 10 {
		t.Fatalf("unexpected members params: %#v", members)
	}
}
