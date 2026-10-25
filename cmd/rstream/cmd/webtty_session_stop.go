// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/webtty"
	"github.com/spf13/cobra"
)

type webTTYSessionStopResult struct {
	SessionID string `json:"session_id"`
	ExitCode  *int   `json:"exit_code"`
}

var webttySessionsStopCmd = &cobra.Command{
	Use:          "stop <session-id>",
	Short:        "Acquire authorized control and stop a managed WebTTY command",
	GroupID:      "webtty-session-primary",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		timeout, _ := cmd.Flags().GetDuration("timeout")
		if timeout <= 0 || timeout > 65*time.Second {
			return fmt.Errorf("--timeout must be positive and no greater than 65s")
		}
		runtime, err := resolveRuntime(cmd, true, true)
		if err != nil {
			return err
		}
		client, err := newClientFromResolved(runtime.Resolved)
		if err != nil {
			return err
		}
		defer closeRstreamClientLogged(client, slog.Default())
		ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
		defer cancel()
		reason, _ := cmd.Flags().GetString("reason")
		result, stopErr := stopWebTTYSession(ctx, runtime, client, args[0], reason)
		if err := printWebTTYJSON(cmd.OutOrStdout(), result); err != nil {
			return err
		}
		return stopErr
	},
}

func init() {
	webttySessionsStopCmd.Flags().Duration("timeout", 65*time.Second, "maximum wait for authorized control and native completion (up to 65s)")
	webttySessionsStopCmd.Flags().String("reason", "", "audited control request reason")
	webttySessionsCmd.AddCommand(webttySessionsStopCmd)
}

func stopWebTTYSession(ctx context.Context, runtime *resolvedRuntime, client *rstream.Client, sessionID, reason string) (result webTTYSessionStopResult, stopErr error) {
	ctx, cancel := context.WithTimeout(ctx, 65*time.Second)
	defer cancel()
	result.SessionID = sessionID
	session, err := client.GetWebTTYSession(ctx, sessionID)
	if err != nil {
		return result, fmt.Errorf("failed to read WebTTY session: %w", err)
	}
	if session.Status == rstream.WebTTYSessionStatusClosed || session.Status == rstream.WebTTYSessionStatusErrored {
		if session.ExitCode == nil {
			return result, fmt.Errorf("WebTTY session has ended without a confirmed process result")
		}
		code := int(*session.ExitCode)
		result.ExitCode = &code
		return result, nil
	}
	if err := validateWebTTYSessionAttachSupport(session); err != nil {
		return result, err
	}
	capabilities, err := client.GetWebTTYCapabilities(ctx)
	if err != nil {
		return result, fmt.Errorf("failed to read WebTTY capabilities: %w", err)
	}
	if !capabilities.ManagedProtocol || !capabilities.LiveAttach || !capabilities.ControlTransfer || !capabilities.ExplicitTermination {
		return result, fmt.Errorf("managed WebTTY termination is not available on this engine")
	}
	crypto, err := webTTYSessionAttachPayloadCrypto(ctx, runtime, client, session)
	if err != nil {
		return result, err
	}
	participant, err := client.AttachWebTTYParticipant(ctx, sessionID, webTTYSessionAttachRequest(crypto))
	if err != nil {
		return result, fmt.Errorf("failed to attach WebTTY participant: %w", err)
	}
	defer func() {
		detachCtx, cancel := webTTYDetachContext(ctx)
		defer cancel()
		_, err := client.DetachWebTTYParticipant(detachCtx, sessionID, participant.ID, rstream.DetachWebTTYParticipantRequest{Reason: "stop reader disconnected"})
		if err != nil && stopErr == nil {
			stopErr = fmt.Errorf("failed to detach WebTTY stop reader: %w", err)
		}
	}()
	streamURL, err := webTTYParticipantStreamURL(ctx, client, sessionID, participant.ID)
	if err != nil {
		return result, err
	}
	stream, err := webtty.OpenClientSession(ctx, &webtty.SessionConfig{
		URL:               streamURL,
		Transport:         webtty.WebTTYTransportWebSocket,
		DialTLSContext:    newWebTTYEngineDialTLSContext(client),
		AuthToken:         webTTYClientAuthToken(client),
		Managed:           &webtty.ManagedSessionConfig{Capabilities: *capabilities},
		Attach:            webTTYAttachConfigFromParticipant(session, participant, []webtty.AttachCapability{webtty.AttachCapabilityReadStream, webtty.AttachCapabilityRequestControl, webtty.AttachCapabilityReceiveControl}),
		PayloadCrypto:     webTTYSessionAttachPayloadCryptoValue(crypto),
		EndpointIdentity:  webTTYSessionAttachEndpointIdentity(crypto),
		ClientCredential:  webTTYSessionAttachClientCredential(crypto),
		ClientPrincipalID: webTTYSessionAttachClientPrincipalID(crypto),
		ClientDeviceID:    webTTYSessionAttachDeviceID(crypto),
		ClientBrowserID:   webTTYSessionAttachBrowserID(crypto),
	})
	if err != nil {
		return result, err
	}
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range stream.Events() {
		}
	}()
	defer func() {
		_ = stream.Close()
		<-drained
		if code, err := stream.Wait(); err == nil {
			result.ExitCode = &code
		}
	}()
	if !stream.CanTerminate() {
		return result, webtty.ErrTerminationUnsupported
	}
	request, err := client.CreateWebTTYControlRequest(ctx, sessionID, rstream.CreateWebTTYControlRequest{ParticipantID: participant.ID, Reason: reason})
	if err != nil {
		return result, fmt.Errorf("failed to request WebTTY control: %w", err)
	}
	grant, err := client.ResolveWebTTYControlRequest(ctx, sessionID, request.ID, rstream.ResolveWebTTYControlRequest{Action: "grant", Reason: reason})
	if err != nil {
		return result, fmt.Errorf("failed to acquire authorized WebTTY control: %w", err)
	}
	if grant.Status != rstream.WebTTYControlRequestGranted {
		return result, fmt.Errorf("WebTTY control request was not granted")
	}
	if err := stream.RequestTermination(ctx); err != nil {
		return result, err
	}
	code, err := stream.Wait()
	if err != nil {
		return result, fmt.Errorf("WebTTY termination is unconfirmed: %w", err)
	}
	result.ExitCode = &code
	return result, nil
}
