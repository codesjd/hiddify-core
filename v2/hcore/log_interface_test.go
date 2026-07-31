package hcore

import (
	"testing"

	boxlog "github.com/sagernet/sing-box/log"
)

// recordingStdLogger stands in for sing-box's package-level std logger (hiddify-sing-box's
// log/export.go "std" var) to detect whether LogInterface.WriteMessage ever calls back into it.
// Embedding the nil ContextLogger means any method not overridden here panics if called - a
// deliberate tripwire, since WriteMessage must not reach any of them.
type recordingStdLogger struct {
	boxlog.ContextLogger
	called bool
}

func (r *recordingStdLogger) Debug(args ...any) { r.called = true }

// TestLogInterfaceWriteMessageDoesNotRecurse guards against the root cause of the app hanging at
// "Connecting..." whenever debug/trace log level was enabled: LogInterface.WriteMessage (invoked
// by sing-box's logger as a log.PlatformWriter callback) used to call Log(), which called back
// into the sing-box package-level log.Debug/Info/etc - the same global "std" logger that, once
// pointed at the per-box logger with this same platformWriter registered, re-invokes this exact
// callback, recursing forever. A real goroutine dump from the field showed this exact cycle
// repeated over 166,000 stacked frames on a single goroutine. WriteMessage must publish to the
// app-level log stream directly instead of re-entering the sing-box logger.
func TestLogInterfaceWriteMessageDoesNotRecurse(t *testing.T) {
	original := boxlog.StdLogger()
	recorder := &recordingStdLogger{}
	boxlog.SetStdLogger(recorder)
	defer boxlog.SetStdLogger(original)

	originalLevel := static.logLevel
	static.logLevel = LogLevel_TRACE
	defer func() { static.logLevel = originalLevel }()

	sub := static.logObserver.Subscribe(1)
	defer static.logObserver.Unsubscribe(sub)

	(&LogInterface{}).WriteMessage(boxlog.LevelDebug, "test message")

	if recorder.called {
		t.Fatal("WriteMessage called back into the sing-box std logger - this recurses forever once std is the same per-box logger that invokes this callback")
	}

	select {
	case msg := <-sub:
		if msg.Message != "test message" {
			t.Fatalf("expected the published message to be %q, got %q", "test message", msg.Message)
		}
	default:
		t.Fatal("expected WriteMessage to publish to the app-level log observer")
	}
}
