package hcore

import (
	"context"
	"time"

	"github.com/hiddify/hiddify-core/v2/config"
	"github.com/sagernet/sing-box/log"
)

func (s *CoreService) Restart(ctx context.Context, in *StartRequest) (*CoreInfoResponse, error) {
	return Restart(static.BaseContext, in)
}

func Restart(ctx context.Context, in *StartRequest) (coreResponse *CoreInfoResponse, err error) {
	defer config.DeferPanicToError("startmobile", func(recovered_err error) {
		coreResponse, err = errorWrapper(MessageType_UNEXPECTED_ERROR, recovered_err)
	})
	log.Debug("[Service] Restarting")
	// if static.CoreState != CoreStates_STARTED {
	// 	return errorWrapper(MessageType_INSTANCE_NOT_STARTED, fmt.Errorf("instance not started"))
	// }
	// if static.Box == nil {
	// 	return errorWrapper(MessageType_INSTANCE_NOT_FOUND, fmt.Errorf("instance not found"))
	// }

	resp, err := Stop()
	if err != nil {
		return resp, err
	}

	// TUN teardown (interface/route/WFP session close) isn't guaranteed complete the instant
	// Stop() returns - this settling window was previously Android-only, but the same
	// symptom (a config that works once, then every subsequent reconnect in TUN mode fails,
	// with no crash involved) reproduces on Windows too: StartService() below recreates the
	// TUN interface immediately after Stop(), racing the OS's own teardown of the one Stop()
	// just closed.
	static.optionsLock.Lock()
	opts := static.HiddifyOptions
	static.optionsLock.Unlock()
	if opts.EnableTun {
		select {
		case <-ctx.Done():
			return SetCoreStatus(CoreStates_STOPPED, MessageType_INSTANCE_NOT_STARTED, "restart cancelled"), nil
		case <-time.After(time.Second):
		}
	}
	return StartService(ctx, in)
}
