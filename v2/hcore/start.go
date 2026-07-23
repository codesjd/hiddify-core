package hcore

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/hiddify/hiddify-core/v2/config"
	"github.com/hiddify/hiddify-core/v2/db"
	hcommon "github.com/hiddify/hiddify-core/v2/hcommon"
	service_manager "github.com/hiddify/hiddify-core/v2/service_manager"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/experimental/libbox"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/service"
)

// startHangWatchdogTimeout is how long NewService is given before the watchdog dumps a
// goroutine trace. Well above any legitimate startup time (even a large subscription's worth of
// sequential outbound construction), so it only fires on a genuine stall.
const startHangWatchdogTimeout = 20 * time.Second

func (s *CoreService) Start(ctx context.Context, in *StartRequest) (*CoreInfoResponse, error) {
	return Start(static.BaseContext, in)
}

func Start(ctx context.Context, in *StartRequest) (*CoreInfoResponse, error) {
	return StartService(ctx, in)
}

func (s *CoreService) StartService(ctx context.Context, in *StartRequest) (*CoreInfoResponse, error) {
	return StartService(ctx, in)
}

func saveLastStartRequest(in *StartRequest) error {
	if in.ConfigContent == "" && in.ConfigPath == "" {
		return nil
	}
	settings := db.GetTable[hcommon.AppSettings]()
	return settings.UpdateInsert(
		&hcommon.AppSettings{
			Id:    "lastStartRequestPath",
			Value: in.ConfigPath,
		},
		&hcommon.AppSettings{
			Id:    "lastStartRequestContent",
			Value: in.ConfigContent,
		},
		&hcommon.AppSettings{
			Id:    "lastStartRequestName",
			Value: in.ConfigName,
		},
	)
}

func loadLastStartRequestIfNeeded(in *StartRequest) (*StartRequest, error) {
	if in != nil && (in.ConfigContent != "" || in.ConfigPath != "") {
		return in, nil
	}
	settings := db.GetTable[hcommon.AppSettings]()
	lastPath, err := settings.Get("lastStartRequestPath")
	if err != nil {
		return nil, err
	}
	lastContent, err := settings.Get("lastStartRequestContent")
	if err != nil {
		return nil, err
	}

	lastName, err := settings.Get("lastStartRequestName")
	if err != nil {
		return nil, err
	}
	return &StartRequest{
		ConfigPath:    lastPath.Value.(string),
		ConfigContent: lastContent.Value.(string),
		ConfigName:    lastName.Value.(string),
	}, nil
}

func StartService(ctx context.Context, in *StartRequest) (coreResponse *CoreInfoResponse, err error) {
	defer config.DeferPanicToError("startmobile", func(recovered_err error) {
		coreResponse, err = errorWrapper(MessageType_UNEXPECTED_ERROR, recovered_err)
	})
	static.lock.Lock()
	defer static.lock.Unlock()

	if static.CoreState != CoreStates_STOPPED {
		// return errorWrapper(MessageType_ALREADY_STARTED, fmt.Errorf("instance already started"))
		return &CoreInfoResponse{
			CoreState:   static.CoreState,
			MessageType: MessageType_ALREADY_STARTED,
			Message:     "instance already started",
		}, nil
	}
	SetCoreStatus(CoreStates_STARTING, MessageType_EMPTY, "")

	in, err = loadLastStartRequestIfNeeded(in)
	if err != nil {
		return errorWrapper(MessageType_ERROR_BUILDING_CONFIG, err)
	}

	static.previousStartRequest = in

	if static.HiddifyOptions == nil {
		return errorWrapper(
			MessageType_ERROR_BUILDING_CONFIG,
			errors.New("HiddifyOptions not initialized"),
		)
	}

	options, err := BuildConfig(ctx, in)
	if err != nil {
		return errorWrapper(MessageType_ERROR_BUILDING_CONFIG, err)
	}
	saveLastStartRequest(in)

	Log(LogLevel_DEBUG, LogType_CORE, "Main Service pre start")
	if err := service_manager.OnMainServicePreStart(options); err != nil {
		return errorWrapper(MessageType_ERROR_EXTENSION, err)
	}
	currentBuildConfigPath := filepath.Join(sWorkingPath, "data/current-config.json")
	Log(LogLevel_DEBUG, LogType_CORE, "Saving config to ", currentBuildConfigPath)

	config.SaveCurrentConfig(ctx, currentBuildConfigPath, *options)
	if static.debug {
		pout, err := options.MarshalJSONContext(ctx)
		if err != nil {
			return errorWrapper(MessageType_ERROR_BUILDING_CONFIG, err)
		}
		Log(LogLevel_INFO, LogType_CORE, "Current Config is:\n", string(pout))
	}
	ctx = libbox.FromContext(ctx, static.globalPlatformInterface)
	if static.globalPlatformInterface != nil {
		platformWrapper := libbox.WrapPlatformInterface(static.globalPlatformInterface)
		service.MustRegister[adapter.PlatformInterface](ctx, platformWrapper)
		// } else {
		// 	service.MustRegister[adapter.PlatformInterface](ctx, (*adapter.PlatformInterface)nil)
	}
	Log(LogLevel_DEBUG, LogType_CORE, "Stating Service with delay ?", in.DelayStart)
	if in.DelayStart {
		<-time.After(1000 * time.Millisecond)
	}
	// libbox.SetMemoryLimit(bool) was removed from this sing-box version. Memory limiting now
	// happens inside libbox.Setup() itself (already called during service init, see
	// grpc_server.go), which applies iOS's ~50MB Network Extension cap automatically via
	// oomkiller.DefaultAppleNetworkExtensionMemoryLimit when C.IsIos, and otherwise leaves Go's
	// GC unrestricted. The removed function's non-iOS default limit value (used here whenever
	// DisableMemoryLimit was false) isn't available anywhere in the current library, and
	// guessing a number for it risks capping desktop/Android far too low (causing GC thrashing
	// under normal use) - deliberately left as Go's default (no artificial limit) rather than a
	// blind guess. TODO: recover the intended non-iOS default from hiddify-core's release
	// history and restore it here.
	// NewService can block indefinitely (observed: users report the app stuck at "Connecting..."
	// specifically at debug/trace log levels, requiring a force-close). The existing
	// goroutine-start.log dump below only fires *after* NewService returns, so it captures
	// nothing if NewService itself is what's hung. This watchdog dumps a full goroutine trace
	// from a separate goroutine if NewService hasn't returned within startHangWatchdogTimeout,
	// without affecting NewService's own execution - purely diagnostic, so the next reproduction
	// pinpoints exactly which goroutine is stuck and where, rather than more guessing.
	watchdogDone := make(chan struct{})
	go func() {
		select {
		case <-watchdogDone:
		case <-time.After(startHangWatchdogTimeout):
			dumpGoroutinesToFile(fmt.Sprint(sWorkingPath, "/data/goroutine-hang-watchdog.log"))
		}
	}()
	instance, err := NewService(ctx, *options)
	close(watchdogDone)
	if err != nil {
		return errorWrapper(MessageType_START_SERVICE, err)
	}
	static.StartedService = instance
	if static.debug {
		dumpGoroutinesToFile(fmt.Sprint(sWorkingPath, "/data/goroutine-start.log"))
	}
	for inb := range options.Inbounds {
		if opts, ok := options.Inbounds[inb].Options.(option.SocksInboundOptions); ok {
			static.ListenPort = opts.ListenPort
		}
	}

	return SetCoreStatus(CoreStates_STARTED, MessageType_EMPTY, ""), nil
}
