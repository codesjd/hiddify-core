package hcore

import (
	"fmt"
	"os"
	"runtime/pprof"
	"time"

	"github.com/sagernet/sing-box/log"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func logLevel(level LogLevel, msg string) {
	switch level {
	case LogLevel_FATAL:
		log.Error(msg)
	case LogLevel_TRACE:
		log.Trace(msg)
	case LogLevel_DEBUG:
		log.Debug(msg)
	case LogLevel_INFO:
		log.Info(msg)
	case LogLevel_WARNING:
		log.Warn(msg)
	case LogLevel_ERROR:
		log.Error(msg)
	default:
		log.Debug(msg)
	}
}
func Log(level LogLevel, typ LogType, message ...any) {
	if level < static.logLevel {
		return
	}
	// if static.debug {
	msg := fmt.Sprintf("H %v %v", typ, fmt.Sprint(message...))
	logLevel(level, msg)
	// fmt.Printf("%v %v %v\n", level, typ, fmt.Sprint(message...))
	// os.Stderr.WriteString(fmt.Sprintf("%v %v %v\n", level, typ, fmt.Sprint(message...)))
	// }

	static.logObserver.Publish(&LogMessage{
		Level:   level,
		Type:    typ,
		Time:    timestamppb.New(time.Now()),
		Message: fmt.Sprint(message...),
	})
}

// publishServiceLogMessage forwards a message to the app-level log stream/UI (the gRPC
// LogListener subscribers) without re-entering the sing-box logger, unlike Log(). It exists
// specifically for LogInterface.WriteMessage (sing-box's log.PlatformWriter callback): that
// callback fires *from inside* the box's own logger (sing-box/log.(*observableLogger).Log calling
// its registered platformWriter) whenever the box's global std logger has been pointed at that
// same per-box logger instance. Log() calls logLevel(), which calls back into the sing-box
// package-level log.Debug/Info/etc - i.e. right back into std, which invokes the platformWriter
// callback again, forever. Observed as the root cause of the app getting stuck at "Connecting..."
// specifically at debug/trace log levels: a goroutine dump showed this exact cycle repeated over
// 166,000 times on one goroutine (daemon.StartedService.WriteMessage only invokes
// WriteDebugMessage at all when static.debug is true, i.e. debug/trace - explaining why it never
// happened at info level, regardless of profile or profile count).
func publishServiceLogMessage(level LogLevel, typ LogType, message string) {
	if level < static.logLevel {
		return
	}
	static.logObserver.Publish(&LogMessage{
		Level:   level,
		Type:    typ,
		Time:    timestamppb.New(time.Now()),
		Message: message,
	})
}

func (s *CoreService) LogListener(req *LogRequest, stream grpc.ServerStreamingServer[LogMessage]) error {
	logSub := static.logObserver.Subscribe(1)
	defer static.logObserver.Unsubscribe(logSub)

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case info := <-logSub:
			if info.Level < req.Level {
				continue
			}
			stream.Send(info)
			// case <-time.After(500 * time.Millisecond):
		}
	}
}

func dumpGoroutinesToFile(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	return pprof.Lookup("goroutine").WriteTo(f, 2)
}
