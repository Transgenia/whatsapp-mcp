package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

type blockedQRBridge struct{ fakeBridge }

func (f *blockedQRBridge) QRChannel(ctx context.Context) (<-chan pairingEvent, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestDeadlineCoversQRChannelInitialization(t *testing.T) {
	f := &blockedQRBridge{}
	started := time.Now()
	stop, err := startBridgeSession(context.Background(), f, true, 20*time.Millisecond, sessionSignals{}, nil)
	if stop != nil {
		stop()
	}
	if err != errStartupTimeout {
		t.Fatalf("got %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("QR initialization ignored startup deadline")
	}
	if f.connectCalls != 0 {
		t.Fatal("connect was called after QR initialization timed out")
	}
}

type fakeBridge struct {
	linked, ready         bool
	qr                    <-chan pairingEvent
	qrErr, connectErr     error
	qrCalls, connectCalls int
	connect               func(context.Context) error
}

func (f *fakeBridge) HasSession() bool { return f.linked }
func (f *fakeBridge) Ready() bool      { return f.ready }
func (f *fakeBridge) QRChannel(context.Context) (<-chan pairingEvent, error) {
	f.qrCalls++
	return f.qr, f.qrErr
}
func (f *fakeBridge) Connect(ctx context.Context) error {
	f.connectCalls++
	if f.connect != nil {
		return f.connect(ctx)
	}
	return f.connectErr
}

func qrEvents(events ...pairingEvent) <-chan pairingEvent {
	ch := make(chan pairingEvent, len(events))
	for _, event := range events {
		ch <- event
	}
	close(ch)
	return ch
}

// Most tests release successful simulated connections immediately. Production
// keeps the returned stop function until shutdown or terminal session logout.
func startBridge(ctx context.Context, f bridgeConnection, allow bool, timeout time.Duration, signals sessionSignals, render func(string)) error {
	stop, err := startBridgeSession(ctx, f, allow, timeout, signals, render)
	if stop != nil {
		stop()
	}
	return err
}

func TestSuccessRetainsConnectionContextUntilExplicitStop(t *testing.T) {
	var connectionCtx context.Context
	f := &fakeBridge{linked: true, ready: true, connect: func(ctx context.Context) error { connectionCtx = ctx; return nil }}
	stop, err := startBridgeSession(context.Background(), f, false, time.Second, sessionSignals{}, nil)
	if err != nil || stop == nil {
		t.Fatalf("startup failed: %v", err)
	}
	defer stop()
	if connectionCtx.Err() != nil {
		t.Fatal("startup completion canceled SDK keep-alive context")
	}
	stop()
	if connectionCtx.Err() != context.Canceled {
		t.Fatal("explicit shutdown did not cancel SDK context")
	}
}

func TestTimeoutCancelsConnectionContext(t *testing.T) {
	observed := make(chan context.Context, 1)
	f := &fakeBridge{linked: true, connect: func(ctx context.Context) error { observed <- ctx; <-ctx.Done(); return ctx.Err() }}
	stop, err := startBridgeSession(context.Background(), f, false, 20*time.Millisecond, sessionSignals{}, nil)
	if err != errStartupTimeout || stop != nil {
		t.Fatalf("got %v", err)
	}
	select {
	case ctx := <-observed:
		if ctx.Err() != context.Canceled {
			t.Fatal("failed attempt retained SDK context")
		}
	case <-time.After(time.Second):
		t.Fatal("fake connect not entered")
	}
}

// Test-only reproduction of the original range-before-timeout defect. Setting
// BRIDGE_TEST_LEGACY_QR_WAIT=1 deliberately runs the old waiting pattern and must
// fail this one test; channel cleanup prevents a hung test process.
func TestQRDeadlineRegression(t *testing.T) {
	qr := make(chan pairingEvent)
	done := make(chan error, 1)
	go func() {
		if os.Getenv("BRIDGE_TEST_LEGACY_QR_WAIT") == "1" {
			for range qr {
			}
			<-time.After(20 * time.Millisecond)
			done <- errStartupTimeout
			return
		}
		done <- startBridge(context.Background(), &fakeBridge{qr: qr}, true, 20*time.Millisecond, sessionSignals{}, nil)
	}()
	select {
	case err := <-done:
		close(qr)
		if err != errStartupTimeout {
			t.Fatalf("got %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		close(qr)
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("test worker did not stop")
		}
		t.Fatal("QR deadline was not applied while the event channel remained open")
	}
}

func TestPairingRequiresExplicitOptIn(t *testing.T) {
	for _, value := range []string{"", "false", "TRUE", "1", " true ", "yes"} {
		t.Run(value, func(t *testing.T) {
			f := &fakeBridge{qr: qrEvents(pairingEvent{Kind: "code", Code: "must-not-render"})}
			renders := 0
			err := startBridge(context.Background(), f, pairingEnabled(value), time.Second, sessionSignals{}, func(string) { renders++ })
			if err != errPairingRequired || f.qrCalls != 0 || f.connectCalls != 0 || renders != 0 {
				t.Fatalf("unexpected result: err=%v qr=%d connect=%d render=%d", err, f.qrCalls, f.connectCalls, renders)
			}
		})
	}
	if !pairingEnabled("true") {
		t.Fatal("explicit true must enable pairing")
	}
}

func TestQRInitializationFailureDoesNotConnectOrLeak(t *testing.T) {
	f := &fakeBridge{qrErr: errors.New("private-remote-error")}
	err := startBridge(context.Background(), f, true, time.Second, sessionSignals{}, nil)
	if err != errQRChannel || f.connectCalls != 0 || strings.Contains(err.Error(), "private") {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestNilQRChannelDoesNotConnect(t *testing.T) {
	f := &fakeBridge{}
	if err := startBridge(context.Background(), f, true, time.Second, sessionSignals{}, nil); err != errQRChannel || f.connectCalls != 0 {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestClosedQRChannelWithoutSuccessFails(t *testing.T) {
	f := &fakeBridge{qr: qrEvents()}
	if err := startBridge(context.Background(), f, true, time.Second, sessionSignals{}, nil); err != errQRClosed {
		t.Fatalf("got %v", err)
	}
}

func TestTerminalQREventsFailClosed(t *testing.T) {
	for event, expected := range map[string]error{
		"timeout":                         errQRTimeout,
		"error":                           errQRRejected,
		"err-client-outdated":             errQRClientOutdated,
		"err-unexpected-state":            errQRUnexpectedState,
		"err-scanned-without-multidevice": errQRRejected,
		"unrecognized-private-event":      errQRRejected,
	} {
		t.Run(event, func(t *testing.T) {
			f := &fakeBridge{qr: qrEvents(pairingEvent{Kind: event})}
			err := startBridge(context.Background(), f, true, time.Second, sessionSignals{}, nil)
			if err != expected || strings.Contains(err.Error(), event) {
				t.Fatalf("got %v; wanted %v", err, expected)
			}
		})
	}
}

func TestDeadlineCoversSilentQRChannel(t *testing.T) {
	f := &fakeBridge{qr: make(chan pairingEvent)}
	started := time.Now()
	err := startBridge(context.Background(), f, true, 20*time.Millisecond, sessionSignals{}, nil)
	if err != errStartupTimeout || time.Since(started) > time.Second {
		t.Fatalf("unbounded wait or wrong error: %v", err)
	}
}

func TestDeadlineCoversConnect(t *testing.T) {
	f := &fakeBridge{linked: true, connect: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}
	err := startBridge(context.Background(), f, false, 20*time.Millisecond, sessionSignals{}, nil)
	if err != errStartupTimeout {
		t.Fatalf("got %v", err)
	}
}

func TestCancellationStopsStartup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &fakeBridge{qr: make(chan pairingEvent)}
	err := startBridge(ctx, f, true, time.Second, sessionSignals{}, nil)
	if err != errStartupCancelled || f.connectCalls != 0 || f.qrCalls != 0 {
		t.Fatalf("got %v", err)
	}
}

func TestConnectFailureIsSanitized(t *testing.T) {
	f := &fakeBridge{linked: true, connectErr: errors.New("private socket address")}
	if err := startBridge(context.Background(), f, false, time.Second, sessionSignals{}, nil); err != errConnectFailed {
		t.Fatalf("got %v", err)
	}
}

func TestPairingSuccessNeedsAuthenticatedTransport(t *testing.T) {
	f := &fakeBridge{qr: qrEvents(pairingEvent{Kind: "success"})}
	if err := startBridge(context.Background(), f, true, 20*time.Millisecond, sessionSignals{}, nil); err != errStartupTimeout {
		t.Fatalf("got %v", err)
	}
}

func TestPairingSuccessAndRendering(t *testing.T) {
	f := &fakeBridge{ready: true, qr: qrEvents(pairingEvent{Kind: "code", Code: "synthetic-qr"}, pairingEvent{Kind: "success"})}
	rendered := ""
	err := startBridge(context.Background(), f, true, time.Second, sessionSignals{}, func(code string) { rendered = code })
	if err != nil || rendered != "synthetic-qr" || f.connectCalls != 1 {
		t.Fatalf("result %v, rendered %q", err, rendered)
	}
}

func TestLinkedReadyClientDoesNotRequestQR(t *testing.T) {
	f := &fakeBridge{linked: true, ready: true}
	err := startBridge(context.Background(), f, false, time.Second, sessionSignals{}, nil)
	if err != nil || f.qrCalls != 0 || f.connectCalls != 1 {
		t.Fatalf("got %v", err)
	}
}

func TestLoggedOutStopsStartup(t *testing.T) {
	loggedOut := make(chan struct{})
	close(loggedOut)
	f := &fakeBridge{linked: true}
	err := startBridge(context.Background(), f, false, time.Second, sessionSignals{LoggedOut: loggedOut}, nil)
	if err != errLoggedOut {
		t.Fatalf("got %v", err)
	}
}

func TestEmptyQRIsRejected(t *testing.T) {
	f := &fakeBridge{qr: qrEvents(pairingEvent{Kind: "code"})}
	if err := startBridge(context.Background(), f, true, time.Second, sessionSignals{}, nil); err != errQRRejected {
		t.Fatalf("got %v", err)
	}
}

func TestSessionRevocationCannotBeClearedByLateConnectedEvent(t *testing.T) {
	state := newSessionState()
	if !state.Allowed(true, true) {
		t.Fatal("authenticated session rejected")
	}
	if state.Allowed(true, false) || state.Allowed(false, true) {
		t.Fatal("unauthenticated transport allowed")
	}
	state.MarkLoggedOut()
	state.MarkConnected()
	state.MarkLoggedOut()
	if state.Allowed(true, true) {
		t.Fatal("logged-out session re-enabled")
	}
	select {
	case <-state.Signals().LoggedOut:
	default:
		t.Fatal("logged-out notification missing")
	}
}

func TestLoggedOutWinsOverReadyTransport(t *testing.T) {
	state := newSessionState()
	state.MarkLoggedOut()
	f := &fakeBridge{linked: true, ready: true}
	if err := startBridge(context.Background(), f, false, time.Second, state.Signals(), nil); err != errLoggedOut {
		t.Fatalf("got %v", err)
	}
}

func TestCancellationWhileWaitingForQR(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	f := &fakeBridge{qr: make(chan pairingEvent), connect: func(context.Context) error { cancel(); return nil }}
	if err := startBridge(ctx, f, true, time.Second, sessionSignals{}, nil); err != errStartupCancelled {
		t.Fatalf("got %v", err)
	}
}

func TestReadyNotificationCompletesLinkedStartup(t *testing.T) {
	state := newSessionState()
	f := &fakeBridge{linked: true}
	f.connect = func(context.Context) error { f.ready = true; state.MarkConnected(); return nil }
	if err := startBridge(context.Background(), f, false, time.Second, state.Signals(), nil); err != nil {
		t.Fatalf("got %v", err)
	}
}
