// See LICENSE file in the project root for license information.

package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

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

func TestMCPToolsListContainsWebTTYTools(t *testing.T) {
	response := handleMCPMessage(t.Context(), mcpMessage{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/list"})
	if response.Error != nil {
		t.Fatalf("unexpected error: %#v", response.Error)
	}
	payload, err := json.Marshal(response.Result)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	for _, want := range []string{"rstream_webtty_list", "rstream_webtty_exec", "rstream_webtty_fs_read"} {
		if !strings.Contains(string(payload), want) {
			t.Fatalf("tools/list missing %q: %s", want, string(payload))
		}
	}
}

func TestMCPInitializeProtocolVersion(t *testing.T) {
	response := handleMCPMessage(t.Context(), mcpMessage{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "initialize"})
	if response.Error != nil {
		t.Fatalf("unexpected error: %#v", response.Error)
	}
	payload, err := json.Marshal(response.Result)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if !strings.Contains(string(payload), `"protocolVersion":"2025-06-18"`) {
		t.Fatalf("initialize returned unexpected protocol version: %s", string(payload))
	}
}

func TestMCPArgumentHelpers(t *testing.T) {
	args := map[string]json.RawMessage{
		"name": json.RawMessage(`" shell "`),
		"list": json.RawMessage(`[" a ","","b"]`),
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
}
