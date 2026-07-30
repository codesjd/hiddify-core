package tunnelservice

import (
	context "context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"time"

	hcommon "github.com/hiddify/hiddify-core/v2/hcommon"
	hutils "github.com/hiddify/hiddify-core/v2/hutils"
	grpc "google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const tunnelServicePort uint16 = 18020

var (
	tunnelServiceAddress = fmt.Sprintf("127.0.0.1:%d", tunnelServicePort)
	tunnelServiceRunning = false
)

func isSupportedOS() bool {
	return runtime.GOOS == "windows" || runtime.GOOS == "linux"
}

// dialTunnelService dials the tunnel helper's gRPC server and returns a
// context carrying the auth token as outgoing metadata, timed out after
// timeout. Callers must Close() the returned conn and call the returned
// cancel func.
func dialTunnelService(timeout time.Duration) (*grpc.ClientConn, context.Context, context.CancelFunc, error) {
	token, err := hutils.ReadServiceToken(getCurrentExecutableDirectory(), "tunnel")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read tunnel service token: %w", err)
	}
	conn, err := grpc.Dial(tunnelServiceAddress, grpc.WithInsecure())
	if err != nil {
		return nil, nil, nil, err
	}
	ctx := metadata.AppendToOutgoingContext(context.Background(), "token", token)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	return conn, ctx, cancel, nil
}

func ActivateTunnelService(opt *TunnelStartRequest) error {
	tunnelServiceRunning = true
	return startTunnelRequestWithFailover(opt, true)
}

func DeactivateTunnelServiceForce() error {
	return stopTunnelRequest()
}

func DeactivateTunnelService() error {
	if tunnelServiceRunning {
		err := stopTunnelRequest()
		if err != nil {
			tunnelServiceRunning = false
		}
		return err
	} else {
		return stopTunnelRequest()
	}
}

func startTunnelRequestWithFailover(opt *TunnelStartRequest, installService bool) error {
	res, err := startTunnelRequest(opt, installService)
	fmt.Printf("Start Tunnel Result: %v\n", res)
	if err != nil {
		fmt.Printf("Start Tunnel Failed! Stopping hcore... err=%v\n", err)
		// StopAndAlert(pb.MessageType.MessageType_UNEXPECTED_ERROR, "Start Tunnel Failed! Stopping...")
	}
	return err
}

func startTunnelRequest(opt *TunnelStartRequest, installService bool) (bool, error) {
	if !hutils.IsPortInUse(tunnelServicePort) {
		if installService {
			return runTunnelService(opt)
		}
		return false, fmt.Errorf("service is not running")
	}
	conn, ctx, cancel, err := dialTunnelService(time.Second * 5)
	if err != nil {
		log.Printf("did not connect: %v", err)
		return false, err
	}
	defer conn.Close()
	defer cancel()
	c := NewTunnelServiceClient(conn)
	_, _ = c.Stop(ctx, &hcommon.Empty{})
	res, err := c.Start(ctx, opt)
	if err != nil {
		log.Printf("could not greet: %+v %+v", res, err)

		if installService {
			ExitTunnelService()
			return runTunnelService(opt)
		}
		return false, err
	}

	return true, nil
}

func stopTunnelRequest() error {
	conn, ctx, cancel, err := dialTunnelService(time.Second * 20)
	if err != nil {
		log.Printf("did not connect: %v", err)
		return err
	}
	defer conn.Close()
	defer cancel()
	c := NewTunnelServiceClient(conn)

	res, err := c.Stop(ctx, &hcommon.Empty{})
	if err != nil {
		log.Printf("did not Stopped: %v %v", res, err)
		_, _ = c.Stop(ctx, &hcommon.Empty{})
		return err
	}

	return nil
}

func ExitTunnelService() (bool, error) {
	conn, ctx, cancel, err := dialTunnelService(time.Second * 1)
	if err != nil {
		log.Printf("did not connect: %v", err)
		return false, err
	}
	defer conn.Close()
	defer cancel()
	c := NewTunnelServiceClient(conn)

	res, err := c.Exit(ctx, &hcommon.Empty{})
	if res != nil {
		log.Printf("did not exit: %v %v", res, err)
		return false, err
	}

	return true, nil
}

func runTunnelService(opt *TunnelStartRequest) (bool, error) {
	executablePath := getTunnelServicePath()
	fmt.Printf("Executable path is %s", executablePath)
	out, err := hutils.ExecuteCmd(executablePath, false, "tunnel", "install")
	fmt.Println("Shell command executed:", out, err)
	if err != nil {
		out, err = hutils.ExecuteCmd(executablePath, true, "tunnel", "run")
		fmt.Println("Shell command executed without flag:", out, err)
	}
	if err == nil {
		<-time.After(1 * time.Second) // wait until service loaded completely
	}
	return startTunnelRequest(opt, false)
}

func getTunnelServicePath() string {
	var fullPath string
	exePath, _ := os.Executable()
	binFolder := filepath.Dir(exePath)
	switch runtime.GOOS {
	case "windows":
		fullPath = "HiddifyCli.exe"
	case "darwin":
		fallthrough
	default:
		fullPath = "HiddifyCli"
	}

	abspath, _ := filepath.Abs(filepath.Join(binFolder, fullPath))
	return abspath
}
