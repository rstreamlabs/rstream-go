// See LICENSE file in the project root for license information.

package pb

import (
	"bytes"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestAttachMessageRoundTrip(t *testing.T) {
	msg := &Message{Payload: &Message_Attach{Attach: &Attach{
		SessionId:     "session-1",
		ParticipantId: "participant-1",
		AttachGrant:   []byte("grant"),
		RequestedRole: AttachRole_ATTACH_ROLE_SPECTATOR,
		Transport:     AttachTransport_ATTACH_TRANSPORT_WEBSOCKET,
		Capabilities: []AttachCapability{
			AttachCapability_ATTACH_CAPABILITY_READ_STREAM,
			AttachCapability_ATTACH_CAPABILITY_REQUEST_CONTROL,
		},
		DeviceId:  wrapperspb.String("device-1"),
		BrowserId: wrapperspb.String("browser-1"),
	}}}
	encoded, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded Message
	if err := proto.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	attach := decoded.GetAttach()
	if attach == nil {
		t.Fatal("decoded message is missing attach payload")
	}
	if attach.SessionId != "session-1" || attach.ParticipantId != "participant-1" {
		t.Fatalf("unexpected attach identity: %#v", attach)
	}
	if !bytes.Equal(attach.AttachGrant, []byte("grant")) {
		t.Fatalf("unexpected attach grant: %q", attach.AttachGrant)
	}
	if attach.RequestedRole != AttachRole_ATTACH_ROLE_SPECTATOR {
		t.Fatalf("unexpected requested role: %s", attach.RequestedRole)
	}
	if attach.Transport != AttachTransport_ATTACH_TRANSPORT_WEBSOCKET {
		t.Fatalf("unexpected transport: %s", attach.Transport)
	}
	if len(attach.Capabilities) != 2 ||
		attach.Capabilities[0] != AttachCapability_ATTACH_CAPABILITY_READ_STREAM ||
		attach.Capabilities[1] != AttachCapability_ATTACH_CAPABILITY_REQUEST_CONTROL {
		t.Fatalf("unexpected capabilities: %v", attach.Capabilities)
	}
	if attach.GetDeviceId().GetValue() != "device-1" || attach.GetBrowserId().GetValue() != "browser-1" {
		t.Fatalf("unexpected attach peer metadata: %#v", attach)
	}
}
