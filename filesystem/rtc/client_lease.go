// See LICENSE file in the project root for license information.

package rtc

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/pion/webrtc/v4"
)

type restartResult struct {
	err      error
	progress int64
}

func (b *responseBody) lease(config ClientConfig, headers http.Header, session string) {
	renewal := max(time.Second, time.Duration(config.Info.LeaseSeconds)*time.Second/3)
	interval := time.Duration(config.Info.RestartSeconds) * time.Second
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	cadence := restartCadence{restarted: time.Now(), interval: interval}
	results := make(chan restartResult, 1)
	negotiating := false
	timer := time.NewTimer(renewal)
	defer timer.Stop()
	for {
		select {
		case <-b.ctx.Done():
			return
		case result := <-results:
			if result.err != nil {
				b.cancel(fmt.Errorf("filesystem ICE restart: %w", result.err))
				return
			}
			negotiating = false
			cadence.selected(time.Now(), result.progress, b.complete.Load())
			b.recoveryAt.Store(result.progress)
			b.recovering.Store(!b.complete.Load())
			select {
			case b.recovery <- struct{}{}:
			default:
			}
		case <-timer.C:
			restart := cadence.due(time.Now(), b.progress.Load(), b.complete.Load())
			if b.hasHeader.Load() && !negotiating && restart {
				negotiating = true
				b.workers.Add(1)
				go func() {
					defer b.workers.Done()
					ctx, cancel := boundedContext(b.ctx)
					defer cancel()
					err := b.restart(ctx, config, headers, session)
					results <- restartResult{err: err, progress: b.progress.Load()}
				}()
			}
			ctx, cancel := boundedContext(b.ctx)
			err := SignalHTTP(ctx, config.HTTP, config.Endpoint, headers, Signal{Action: "renew", Session: session}, nil)
			cancel()
			if err != nil {
				b.cancel(fmt.Errorf("filesystem authorization renewal: %w", err))
				return
			}
			timer.Reset(renewal)
		}
	}
}

func (b *responseBody) restart(ctx context.Context, config ClientConfig, headers http.Header, session string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, config.Endpoint, nil)
	if err != nil {
		return err
	}
	request.Header = headers.Clone()
	request.Header.Del("Range")
	response, err := HTTPDo(config.HTTP, request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("filesystem ICE refresh failed (%d)", response.StatusCode)
	}
	var info Info
	if err := Decode(response.Body, &info); err != nil {
		return err
	}
	configuration := b.peer.GetConfiguration()
	configuration.ICEServers = info.ICEServers
	if err := b.peer.SetConfiguration(configuration); err != nil {
		return err
	}
	offer, err := b.peer.CreateOffer(&webrtc.OfferOptions{ICERestart: true})
	if err != nil {
		return err
	}
	sdp, err := Gather(ctx, b.peer, offer)
	if err != nil {
		return err
	}
	var answer Answer
	if err := SignalHTTP(ctx, config.HTTP, config.Endpoint, headers, Signal{Action: "restart", Session: session, SDP: sdp}, &answer); err != nil {
		return err
	}
	if err := b.peer.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer.SDP}); err != nil {
		return err
	}
	return b.selection.wait(ctx)
}
