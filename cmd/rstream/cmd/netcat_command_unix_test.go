// See LICENSE file in the project root for license information.

//go:build !windows

package cmd

import (
	"reflect"
	"testing"
)

func TestSplitNetcatCommand(t *testing.T) {
	args := splitNetcatCommand(`ssh -o "ProxyCommand=rstream netcat rstrm://ssh-server" root@host`)
	want := []string{"ssh", "-o", "ProxyCommand=rstream netcat rstrm://ssh-server", "root@host"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("unexpected args: got %#v want %#v", args, want)
	}
}
