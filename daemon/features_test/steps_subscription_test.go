package daemonsteps

// steps_subscription.go — WebSocket subscription step definitions.
//
// Covers:
//   - conversationChanged subscription push
//   - subscription lifecycle (subscribe/unsubscribe)
//   - subscription error handling
//   - ConversationChanged payload shape

import (
	"context"
	"time"

	"github.com/cucumber/godog"
)

func registerSubscriptionSteps(ctx *godog.ScenarioContext, ts *testState) {
	// ---------------------------------------------------------------------------
	// conversationChanged subscription (gui-conversation-subscription.feature)
	// ---------------------------------------------------------------------------

	// "When a new record is appended to the matching JSONL on disk"
	// Requires a real fsnotify watcher and a JSONL file — gap.
	ctx.Step(`^the daemon emits a conversationChanged event within 500ms of the file write$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the payload contains sessionUuid = "abc123"$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the payload contains an updated lastSeenAt timestamp$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the payload contains an updated messageCount$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// Debounce / coalescing — GUI-side client behaviour.
	ctx.Step(`^conversationChanged fires at >5Hz$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^TranscriptView coalesces the burst with a 350ms trailing debounce$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the readTranscript call fires at most once per 350ms burst window$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the browser remains responsive during a fast-writing turn$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// Pending turn state machine — GUI client state.
	ctx.Step(`^conversationChanged fires and readTranscript returns turns\.length > N$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the pending turn's status advances to "received"$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^conversationChanged fires and the fresh turns array contains a new assistant turn$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the assistant turn's timestamp >= the pending turn's sentAt$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the pending turn's status advances to "seen"$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the "seen" bubble fades out after 2 seconds$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the pending turn's status flips to "stalled"$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the bubble renders with "·waiting" indicator at reduced opacity$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// Subscription error / reconnect — GUI transport behaviour.
	ctx.Step(`^TranscriptView logs a console\.warn with "\[transcript\] subscription error:"$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the transcript view continues to display the last-loaded turns$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^when the WebSocket reconnects, the subscription resumes from where it left off$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// Subscription lifecycle — torn down on sessionUuid change.
	ctx.Step(`^the "abc123" subscription is unsubscribed via the returned Unsub handle$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^a new subscription is opened for "def456"$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^no subscription leaks remain after the component unmounts$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// Zero-sessionUuid guard.
	ctx.Step(`^no conversationChanged subscription is opened$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the view loads from path alone when path is non-empty$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// ---------------------------------------------------------------------------
	// Subscription connectivity test — daemon-side only
	// Verifies that the conversationChanged subscription can be opened and
	// acknowledged over the WebSocket transport.
	// ---------------------------------------------------------------------------

	ctx.Step(`^the conversationChanged subscription can be opened$`, func(sCtx context.Context) error {
		if ts.httpServer == nil {
			if err := ts.startServerWithRepo(); err != nil {
				return err
			}
		}
		if ts.wsConn == nil {
			if err := ts.openWS(); err != nil {
				return err
			}
		}
		if err := ts.subscribeGQL("test-sub-1", `subscription { conversationChanged(sessionUuid:"test-uuid") { sessionUuid } }`); err != nil {
			return err
		}
		// Give the daemon a moment to set up the subscription.
		time.Sleep(100 * time.Millisecond)
		return nil
	})

	// ---------------------------------------------------------------------------
	// Subscription payload shape (daemon boundary)
	// ---------------------------------------------------------------------------

	ctx.Step(`^(?:when )?TranscriptView mounts without a sessionUuid prop$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// processes subscription — existing daemon subscription
	ctx.Step(`^the processes subscription pushes data$`, func(sCtx context.Context) error {
		if ts.httpServer == nil {
			if err := ts.startServerWithRepo(); err != nil {
				return err
			}
		}
		if ts.wsConn == nil {
			if err := ts.openWS(); err != nil {
				return err
			}
		}
		if err := ts.subscribeGQL("proc-sub-1", `subscription { processes { id command } }`); err != nil {
			return err
		}
		_, err := ts.waitForNext("proc-sub-1", 5*time.Second)
		return err
	})

	// tmuxSessionsChanged — referenced in gui-error-and-edge-cases
	ctx.Step(`^pending subscriptions \(conversationChanged, tmuxSessionsChanged\) re-subscribe$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// null payload on file removal
	ctx.Step(`^the matching JSONL file is deleted from disk$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the daemon emits a null payload for the subscription$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^TranscriptView does not crash on a null push$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the transcript renders the last-cached turns with an "earlier turns omitted" banner if truncated$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// Houdini cache subscription update
	ctx.Step(`^the next Houdini cache update renders fresh data$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// TranscriptView is subscribed (variant of "has subscribed" above).
	ctx.Step(`^TranscriptView is subscribed to conversationChanged for sessionUuid "([^"]*)"$`, func(sCtx context.Context, uuid string) error {
		return godog.ErrPending
	})
}
