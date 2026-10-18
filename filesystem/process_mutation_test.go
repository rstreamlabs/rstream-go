// See LICENSE file in the project root for license information.

package filesystem

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

type processUploadBody struct{ once sync.Once }

func (b *processUploadBody) Read(p []byte) (int, error) {
	b.once.Do(func() { fmt.Fprintln(os.Stdout, "holding") })
	return os.Stdin.Read(p)
}

func TestMutationProcessWorker(t *testing.T) {
	root := os.Getenv("RSTREAM_MUTATION_ROOT")
	if root == "" {
		return
	}
	_, handler := testLocal(t, root, Policy{})
	request := httptest.NewRequest("PUT", "/fs/file", io.MultiReader(strings.NewReader("child"), &processUploadBody{}))
	request.Header.Set("If-Match", os.Getenv("RSTREAM_MUTATION_REVISION"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 201 {
		t.Fatalf("child upload: %d", response.Code)
	}
}

func TestConditionalWritersAcrossProcessesShareAdmission(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "file", "original")
	_, handler := testLocal(t, root, Policy{})
	revision := fileRequest(handler, "GET", "file", "", "", "").Header().Get("ETag")
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMutationProcessWorker$")
	command.Env = append(os.Environ(), "RSTREAM_MUTATION_ROOT="+root, "RSTREAM_MUTATION_REVISION="+revision)
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { input.Close(); command.Process.Kill(); command.Wait() }()
	reader := bufio.NewReader(output)
	line, err := reader.ReadString('\n')
	if err != nil || line != "holding\n" {
		t.Fatalf("child admission: %q %v", line, err)
	}
	done := make(chan int, 1)
	go func() { done <- fileRequest(handler, "PUT", "file", "parent", "If-Match", revision).Code }()
	select {
	case status := <-done:
		t.Fatalf("second process bypassed the admitted upload: %d", status)
	case <-time.After(100 * time.Millisecond):
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("child failed: %v", err)
	}
	select {
	case status := <-done:
		if status != 412 {
			t.Fatalf("obsolete revision accepted: %d", status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("mutation lock was not released")
	}
	if read := fileRequest(handler, "GET", "file", "", "", ""); read.Code != 200 || read.Body.String() != "child" {
		t.Fatalf("committed content: %d %q", read.Code, read.Body.String())
	}
}

func TestCrossInstanceMutationWaitIsCancellable(t *testing.T) {
	root := t.TempDir()
	first, _ := testLocal(t, root, Policy{})
	second, _ := testLocal(t, root, Policy{})
	release, err := first.lockMutation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	unexpected, err := second.lockMutation(ctx)
	if unexpected != nil {
		unexpected()
		t.Fatal("separate Local bypassed namespace admission")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation: %v", err)
	}
}
