package main

import (
	"os"
	"os/exec"
	"strings"
)

func readOr(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

func platformOSVersion() string {
	if name := parseOSReleasePrettyName(readOr("/etc/os-release")); name != "" {
		return name
	}
	return "Linux"
}

func platformCPU() string {
	if model := parseCPUModel(readOr("/proc/cpuinfo")); model != "" {
		return model
	}
	// arm64 /proc/cpuinfo has no "model name" line (Asahi, ARM SBCs, some
	// cloud fleets) — fall back to the device-tree board model, then lscpu.
	if model := strings.TrimSpace(readOr("/proc/device-tree/model")); model != "" {
		return strings.TrimRight(model, "\x00")
	}
	if out, err := exec.Command("lscpu", "-p=CPU-MODEL").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				return line
			}
		}
	}
	return ""
}

func platformRAMBytes() uint64 {
	return parseMemTotalBytes(readOr("/proc/meminfo"))
}
