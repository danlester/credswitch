package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const agentLabel = "com.credswitch.reap"

// agentPlistPath returns where the LaunchAgent plist lives for this user.
// Anchored to CREDSWITCH_HOME (when set) so tests don't pollute the real
// ~/Library/LaunchAgents directory.
func agentPlistPath() string {
	home := os.Getenv("CREDSWITCH_HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return filepath.Join(home, "Library", "LaunchAgents", agentLabel+".plist")
}

// agentLogPath is where launchd routes the job's stdout/stderr.
func agentLogPath(p Paths) string {
	return filepath.Join(p.MasterDir, "reap.log")
}

const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>reap</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>StartCalendarInterval</key>
    <dict>
        <key>Hour</key>
        <integer>%d</integer>
        <key>Minute</key>
        <integer>%d</integer>
    </dict>
    <key>StandardOutPath</key>
    <string>%s</string>
    <key>StandardErrorPath</key>
    <string>%s</string>
</dict>
</plist>
`

var xmlEscape = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// renderAgentPlist builds the plist text. Pure function so tests can pin it.
func renderAgentPlist(binary, logPath string, hour, minute int) string {
	return fmt.Sprintf(plistTemplate,
		agentLabel,
		xmlEscape.Replace(binary),
		hour, minute,
		xmlEscape.Replace(logPath),
		xmlEscape.Replace(logPath),
	)
}

// writeAgent writes the plist file. Does not touch launchctl — separated so
// tests can exercise file generation without loading anything into launchd.
// Returns the path written.
func writeAgent(p Paths, hour, minute int) (string, error) {
	if hour < 0 || hour > 23 {
		return "", fmt.Errorf("hour must be 0–23, got %d", hour)
	}
	if minute < 0 || minute > 59 {
		return "", fmt.Errorf("minute must be 0–59, got %d", minute)
	}
	binary, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate own binary: %w", err)
	}
	binary, err = filepath.EvalSymlinks(binary)
	if err != nil {
		return "", fmt.Errorf("resolve binary path: %w", err)
	}
	plist := renderAgentPlist(binary, agentLogPath(p), hour, minute)

	path := agentPlistPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := atomicWrite(path, []byte(plist)); err != nil {
		return "", err
	}
	return path, nil
}

// installAgent writes the plist and loads it via launchctl. Idempotent:
// unloads any prior copy first so re-running install picks up new flags.
// macOS-only.
func installAgent(p Paths, hour, minute int) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", fmt.Errorf("install-agent only supported on macOS")
	}
	path, err := writeAgent(p, hour, minute)
	if err != nil {
		return "", err
	}
	// Unload errors are expected when the agent isn't already loaded.
	_ = exec.Command("launchctl", "unload", path).Run()
	if out, err := exec.Command("launchctl", "load", path).CombinedOutput(); err != nil {
		return path, fmt.Errorf("launchctl load: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return path, nil
}

// agentInstalled reports whether the LaunchAgent plist is present on disk.
// install/uninstall keep this in sync with launchd, so file existence is a
// good enough proxy for "auto-reap is active" without shelling out.
func agentInstalled() bool {
	_, err := os.Stat(agentPlistPath())
	return err == nil
}

// uninstallAgent unloads via launchctl and removes the plist. Tolerates a
// missing file so the command is safe to run twice.
func uninstallAgent() (string, error) {
	if runtime.GOOS != "darwin" {
		return "", fmt.Errorf("uninstall-agent only supported on macOS")
	}
	path := agentPlistPath()
	_ = exec.Command("launchctl", "unload", path).Run()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return path, err
	}
	return path, nil
}
