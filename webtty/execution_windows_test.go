// See LICENSE file in the project root for license information.

//go:build windows

package webtty

import (
	"strings"
	"testing"
)

func TestWindowsUsernameMatchesDomainQualifiedNames(t *testing.T) {
	if !windowsUsernameMatches("alice", "DOMAIN\\alice") {
		t.Fatal("expected domain-qualified username to match current short name")
	}
	if windowsUsernameMatches("alice", "DOMAIN\\bob") {
		t.Fatal("expected different domain-qualified username to be rejected")
	}
}

func TestWindowsLoginModeRejectsUserSwitchWithoutTokenProvider(t *testing.T) {
	ui := &UserInfo{Name: "rstream-webtty-other-user"}
	_, err := executionCredentialRequired(WebTTYExecutionModeLogin, ui, &UsernameVariant{})
	if err == nil {
		t.Fatal("expected Windows user switch to fail without an OS token provider")
	}
	if !strings.Contains(err.Error(), "OS user token provider") {
		t.Fatalf("unexpected error: %v", err)
	}
}
