// See LICENSE file in the project root for license information.

package rundocker

import (
	"testing"

	"github.com/docker/docker/api/types/events"
)

func TestShouldTriggerDockerEvent(t *testing.T) {
	cases := []struct {
		name string
		msg  events.Message
		want bool
	}{
		{
			name: "container start",
			msg:  events.Message{Type: "container", Action: "start"},
			want: true,
		},
		{
			name: "container update",
			msg:  events.Message{Type: "container", Action: "update"},
			want: true,
		},
		{
			name: "non container",
			msg:  events.Message{Type: "network", Action: "connect"},
			want: false,
		},
		{
			name: "missing action",
			msg:  events.Message{Type: "container"},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldTriggerDockerEvent(tc.msg); got != tc.want {
				t.Fatalf("expected %v, got %v", tc.want, got)
			}
		})
	}
}
