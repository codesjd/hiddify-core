//go:build android

package hutils

func RedirectStderr(path string) error {
	return redirectStderrToFile(path)
}
