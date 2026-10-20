package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// Startup errors are fixed categories. Never wrap remote errors, JIDs or QR data.
var (
	errPairingRequired   = errors.New("PAIRING_REQUIRED")
	errQRChannel         = errors.New("QR_CHANNEL_UNAVAILABLE")
	errQRClosed          = errors.New("QR_CHANNEL_CLOSED_WITHOUT_SUCCESS")
	errQRRejected        = errors.New("QR_PAIRING_FAILED")
	errQRClientOutdated  = errors.New("CLIENT_OUTDATED")
	errQRTimeout         = errors.New("PAIRING_TIMEOUT")
	errQRUnexpectedState = errors.New("QR_UNEXPECTED_STATE")
	errStartupTimeout    = errors.New("STARTUP_DEADLINE_EXCEEDED")
	errStartupCancelled  = errors.New("STARTUP_CANCELLED")
	errConnectFailed     = errors.New("CONNECTION_FAILED")
	errLoggedOut         = errors.New("SESSION_LOGGED_OUT")
)

type pairingEvent struct {
	Kind string
	Code string // Ephemeral pairing material; used only by the explicit terminal renderer.
}

type bridgeConnection interface {
	HasSession() bool
	Ready() bool
	QRChannel(context.Context) (<-chan pairingEvent, error)
	Connect(context.Context) error
}

type sessionSignals struct {
	Connected <-chan struct{}
	LoggedOut <-chan struct{}
}

// A logout is terminal for this process. A late Connected event must not reopen
// REST operations; relinking is a separate, explicitly enabled operator action.
type sessionState struct {
	connected chan struct{}
	loggedOut chan struct{}
	once      sync.Once
	revoked   atomic.Bool
}

func newSessionState() *sessionState {
	return &sessionState{connected: make(chan struct{}, 1), loggedOut: make(chan struct{})}
}

func (s *sessionState) MarkConnected() {
	select {
	case s.connected <- struct{}{}:
	default:
	}
}

func (s *sessionState) MarkLoggedOut() {
	s.revoked.Store(true)
	s.once.Do(func() { close(s.loggedOut) })
}

func (s *sessionState) Allowed(connected, loggedIn bool) bool {
	return !s.revoked.Load() && connected && loggedIn
}

func (s *sessionState) Signals() sessionSignals {
	return sessionSignals{Connected: s.connected, LoggedOut: s.loggedOut}
}

func pairingEnabled(value string) bool { return value == "true" }

func lifecycleContextError(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return errStartupTimeout
	}
	if ctx.Err() != nil {
		return errStartupCancelled
	}
	return nil
}

func startupInterrupted(ctx context.Context, signals sessionSignals) error {
	if err := lifecycleContextError(ctx); err != nil {
		return err
	}
	select {
	case <-signals.LoggedOut:
		return errLoggedOut
	default:
		return nil
	}
}

// startBridge bounds the *entire* startup, including QR-channel initialization,
// ConnectContext and the QR wait. It never retries, resets, deletes or logs out a
// device. Implementations of Connect/QRChannel must honor their context.
func startBridgeSession(parent context.Context, client bridgeConnection, allowPairing bool, timeout time.Duration, signals sessionSignals, render func(string)) (func(), error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	// Whatsmeow uses its connection context for keep-alive and handler workers.
	// The startup deadline must NOT cancel those workers after successful login.
	connectionCtx, stopConnection := context.WithCancel(parent)
	succeeded := false
	defer func() {
		if !succeeded {
			stopConnection()
		}
	}()
	if err := startupInterrupted(ctx, signals); err != nil {
		return nil, err
	}

	linked := client.HasSession()
	if !linked && !allowPairing {
		return nil, errPairingRequired
	}
	var qr <-chan pairingEvent
	if !linked {
		type qrResult struct {
			channel <-chan pairingEvent
			err     error
		}
		results := make(chan qrResult, 1)
		go func() { ch, err := client.QRChannel(connectionCtx); results <- qrResult{ch, err} }()
		select {
		case <-ctx.Done():
			return nil, lifecycleContextError(ctx)
		case <-signals.LoggedOut:
			return nil, errLoggedOut
		case result := <-results:
			if interrupted := startupInterrupted(ctx, signals); interrupted != nil {
				return nil, interrupted
			}
			if result.err != nil || result.channel == nil {
				return nil, errQRChannel
			}
			qr = result.channel
		}
	}
	connectionResult := make(chan error, 1)
	go func() { connectionResult <- client.Connect(connectionCtx) }()
	select {
	case <-ctx.Done():
		return nil, lifecycleContextError(ctx)
	case <-signals.LoggedOut:
		return nil, errLoggedOut
	case err := <-connectionResult:
		if interrupted := startupInterrupted(ctx, signals); interrupted != nil {
			return nil, interrupted
		}
		if err != nil {
			return nil, errConnectFailed
		}
	}

	paired := linked
	for {
		if err := startupInterrupted(ctx, signals); err != nil {
			return nil, err
		}
		if paired && client.Ready() {
			succeeded = true
			return stopConnection, nil
		}
		select {
		case <-ctx.Done():
			return nil, lifecycleContextError(ctx)
		case <-signals.LoggedOut:
			return nil, errLoggedOut
		case <-signals.Connected:
			// Readiness still requires both transport and authenticated session.
		case event, ok := <-qr:
			if err := startupInterrupted(ctx, signals); err != nil {
				return nil, err
			}
			if !ok {
				return nil, errQRClosed
			}
			switch event.Kind {
			case "code":
				if event.Code == "" {
					return nil, errQRRejected
				}
				if render != nil {
					render(event.Code)
				}
			case "success":
				paired = true
				qr = nil // Success closes the SDK channel before authenticated transport is ready.
			case "err-client-outdated":
				return nil, errQRClientOutdated
			case "timeout":
				return nil, errQRTimeout
			case "err-unexpected-state":
				return nil, errQRUnexpectedState
			default:
				// Unknown and future terminal events are never interpreted as success.
				return nil, errQRRejected
			}
		}
	}
}
