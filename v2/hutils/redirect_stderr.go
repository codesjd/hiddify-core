package hutils

import (
	"os"
	"runtime/debug"
)

// redirectStderrToFile points the runtime's crash output (panics/fatal errors) at path.
//
// This used to delegate to sing-box's experimental/libbox.RedirectStderr, which archived
// previous crash reports and chowned the output file to the service user before doing
// exactly this (debug.SetCrashOutput) - that state (working dir, uid/gid) is private to the
// libbox package and the exported wrapper was removed, so this reimplements just the part
// hutils actually needs directly against the stdlib.
func redirectStderrToFile(path string) error {
	outputFile, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := debug.SetCrashOutput(outputFile, debug.CrashOptions{}); err != nil {
		outputFile.Close()
		os.Remove(outputFile.Name())
		return err
	}
	return nil
}
