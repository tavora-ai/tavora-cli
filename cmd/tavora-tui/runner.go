package main

import (
	"context"
	"log/slog"

	tavora "github.com/tavora-ai/tavora-sdk-go"
)

// runResult is what the goroutine driving RunAgent posts to the channel
// once the SSE stream finishes — either nil error (clean done) or the
// transport-level failure surfaced from the SDK.
type runResult struct{ Err error }

// streamEvent is the discriminated union the TUI consumes. It carries
// either an SDK AgentEvent or a final runResult; the channel is closed
// after runResult is sent.
type streamEvent struct {
	Event  *tavora.AgentEvent
	Result *runResult
}

// runAgent spawns a goroutine that calls client.RunAgent, marshals each
// callback invocation into a streamEvent, and closes the channel when
// done. The caller drives the channel from a Bubble Tea cmd, so the UI
// thread never blocks on the SSE stream.
func runAgent(ctx context.Context, client *tavora.Client, sessionID, message string) <-chan streamEvent {
	out := make(chan streamEvent, 32)

	go func() {
		defer close(out)
		slog.Debug("RunAgent start", "session", sessionID)
		err := client.RunAgent(ctx, sessionID, message, func(evt tavora.AgentEvent) {
			e := evt
			select {
			case <-ctx.Done():
			case out <- streamEvent{Event: &e}:
			}
		})
		if err != nil {
			slog.Error("RunAgent transport failure", "session", sessionID, "err", err)
		}
		select {
		case <-ctx.Done():
		case out <- streamEvent{Result: &runResult{Err: err}}:
		}
	}()

	return out
}
