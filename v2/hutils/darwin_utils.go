//go:build darwin

package hutils

func RedirectStderr(path string) error {
	return redirectStderrToFile(path)
}
