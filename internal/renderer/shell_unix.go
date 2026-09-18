//go:build !windows

package renderer

func shellCommand(command string) (string, []string) {
	return "/bin/sh", []string{"-c", command}
}
