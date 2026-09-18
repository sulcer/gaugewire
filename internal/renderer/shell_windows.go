//go:build windows

package renderer

import "os/exec"

// shellCommand mirrors Claude Code on Windows: Git Bash when bash is on PATH,
// PowerShell otherwise.
func shellCommand(command string) (string, []string) {
	if bash, err := exec.LookPath("bash"); err == nil {
		return bash, []string{"-c", command}
	}
	return "powershell", []string{"-NoProfile", "-Command", command}
}
