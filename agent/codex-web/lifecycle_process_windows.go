//go:build windows

package codexweb

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func inspectManagedProcess(pid int) (command, startTime string, alive bool) {
	out, err := exec.Command("wmic", "process", "where", "ProcessId="+strconv.Itoa(pid),
		"get", "CommandLine,CreationDate", "/format:list").Output()
	if err != nil {
		return "", "", false
	}
	var start string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "CommandLine=") {
			command = strings.TrimPrefix(line, "CommandLine=")
		}
		if strings.HasPrefix(line, "CreationDate=") {
			start = strings.TrimPrefix(line, "CreationDate=")
		}
	}
	return command, start, command != ""
}

func managedProcessStartTime(pid int) string {
	_, start, _ := inspectManagedProcess(pid)
	return start
}

func managedProcessOwnsPort(pid, port int) bool {
	out, err := exec.Command("netstat", "-ano", "-p", "tcp").Output()
	if err != nil {
		return false
	}
	wantPort := fmt.Sprintf(":%d", port)
	wantPID := strconv.Itoa(pid)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 5 && strings.Contains(fields[1], wantPort) && fields[3] == "LISTENING" && fields[4] == wantPID {
			return true
		}
	}
	return false
}

func terminateManagedProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
}
