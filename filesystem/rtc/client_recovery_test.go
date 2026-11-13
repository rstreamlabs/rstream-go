// See LICENSE file in the project root for license information.

package rtc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

type observedSignalingTransport struct {
	observed func(Signal)
}

func (o observedSignalingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method == http.MethodPost {
		if request.GetBody == nil {
			return nil, fmt.Errorf("test signaling body cannot be observed")
		}
		body, err := request.GetBody()
		if err != nil {
			return nil, err
		}
		var signal Signal
		err = Decode(body, &signal)
		closeErr := body.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
		o.observed(signal)
	}
	return http.DefaultTransport.RoundTrip(request)
}

func TestConsumerPacedTURNTransferRetainsAuthorizationAcrossRestarts(t *testing.T) {
	servers := testRelay(t)
	payload := bytes.Repeat([]byte("reliable-recovery\x00"), (8<<20)/18+1)
	service, server := testService(t, ServerConfig{LeaseDuration: 3 * time.Second, RestartInterval: time.Second, ICE: func(context.Context) ([]webrtc.ICEServer, error) { return servers, nil }, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(payload) })})
	var renewals, restarts atomic.Int32
	client := &http.Client{Transport: observedSignalingTransport{observed: func(signal Signal) {
		if signal.Action == "renew" {
			renewals.Add(1)
		}
		if signal.Action == "restart" {
			restarts.Add(1)
		}
	}}}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/fs/paced", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := RoundTrip(request, ClientConfig{HTTP: client, Endpoint: server.URL, Info: Info{ICEServers: servers, LeaseSeconds: 3, RestartSeconds: 1}, RelayOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	hash := sha256.New()
	count := 0
	buffer := make([]byte, ChunkSize)
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			_, _ = hash.Write(buffer[:n])
			count += n
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			t.Fatal(readErr)
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
	want := sha256.Sum256(payload)
	if count != len(payload) || !bytes.Equal(hash.Sum(nil), want[:]) {
		t.Fatal("paced TURN recovery changed or truncated the payload")
	}
	if restarts.Load() == 0 || renewals.Load() <= restarts.Load() {
		t.Fatalf("independent authorization/restarts not exercised: renew=%d restart=%d", renewals.Load(), restarts.Load())
	}
	_ = response.Body.Close()
	awaitClosed(t, service)
}

func TestCloseJoinsInFlightICERestart(t *testing.T) {
	servers := testRelay(t)
	service, server := testService(t, ServerConfig{LeaseDuration: 3 * time.Second, RestartInterval: time.Second, ICE: func(context.Context) ([]webrtc.ICEServer, error) { return servers, nil }, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(make([]byte, 8<<20)) })})
	started := make(chan struct{})
	var once sync.Once
	client := &http.Client{Transport: observedSignalingTransport{observed: func(signal Signal) {
		if signal.Action == "restart" {
			once.Do(func() { close(started) })
		}
	}}}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/fs/close", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := RoundTrip(request, ClientConfig{HTTP: client, Endpoint: server.URL, Info: Info{ICEServers: servers, LeaseSeconds: 3, RestartSeconds: 1}, RelayOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("ICE restart did not start")
	}
	done := make(chan error, 1)
	go func() { done <- response.Body.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("close did not join its owned ICE restart")
	}
	awaitClosed(t, service)
}
