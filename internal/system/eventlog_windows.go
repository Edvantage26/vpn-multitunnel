package system

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// SystemLogEntry represents a log entry from system sources (service.log or Windows Event Log)
type SystemLogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source"`    // "service" or "eventlog"
	Level    string    `json:"level"`     // "info", "warn", "error"
	Message  string    `json:"message"`
}

// serviceLogTimestampRegex matches Go's default log format: "2006/01/02 15:04:05 message"
var serviceLogTimestampRegex = regexp.MustCompile(`^(\d{4}/\d{2}/\d{2}\s+\d{2}:\d{2}:\d{2})\s+(.*)$`)

// GetServiceLogs reads the service.log file and returns parsed log entries
func GetServiceLogs(maxEntries int) []SystemLogEntry {
	logFilePath := filepath.Join(os.Getenv("ProgramData"), "VPNMultiTunnel", "service.log")

	logFile, openError := os.Open(logFilePath)
	if openError != nil {
		return nil
	}
	defer logFile.Close()

	var allEntries []SystemLogEntry
	scanner := bufio.NewScanner(logFile)
	// Increase buffer for long lines
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		lineText := scanner.Text()
		if lineText == "" {
			continue
		}

		logEntry := parseServiceLogLine(lineText)
		allEntries = append(allEntries, logEntry)
	}

	// Return only the last maxEntries
	if len(allEntries) > maxEntries {
		allEntries = allEntries[len(allEntries)-maxEntries:]
	}

	return allEntries
}

// parseServiceLogLine parses a single line from service.log
func parseServiceLogLine(lineText string) SystemLogEntry {
	matches := serviceLogTimestampRegex.FindStringSubmatch(lineText)

	var parsedTimestamp time.Time
	var logMessage string

	if len(matches) == 3 {
		parsedTime, parseError := time.Parse("2006/01/02 15:04:05", matches[1])
		if parseError == nil {
			parsedTimestamp = parsedTime
		}
		logMessage = matches[2]
	} else {
		parsedTimestamp = time.Time{}
		logMessage = lineText
	}

	logLevel := classifyServiceLogLevel(logMessage)

	return SystemLogEntry{
		Timestamp: parsedTimestamp,
		Source:    "service",
		Level:    logLevel,
		Message:  logMessage,
	}
}

// classifyServiceLogLevel guesses the log level from message content
func classifyServiceLogLevel(message string) string {
	lowerMessage := strings.ToLower(message)
	switch {
	case strings.Contains(lowerMessage, "error") || strings.Contains(lowerMessage, "failed") || strings.Contains(lowerMessage, "fatal"):
		return "error"
	case strings.Contains(lowerMessage, "warning") || strings.Contains(lowerMessage, "warn"):
		return "warn"
	default:
		return "info"
	}
}

// GetWindowsEventLogs queries Windows Event Log for entries related to VPN MultiTunnel
// Searches Application log for source "VPNMultiTunnel" and also for app crash events
func GetWindowsEventLogs(maxEntries int, hoursBack int) []SystemLogEntry {
	if hoursBack <= 0 {
		hoursBack = 168 // 7 days default
	}

	var allEntries []SystemLogEntry

	// Query 1: VPNMultiTunnel service events
	serviceEvents := queryEventLog("Application", "VPNMultiTunnel", hoursBack, maxEntries/2)
	allEntries = append(allEntries, serviceEvents...)

	// Query 2: Application crash events mentioning our app
	crashEvents := queryCrashEvents(hoursBack, maxEntries/2)
	allEntries = append(allEntries, crashEvents...)

	// Sort by timestamp descending (newest first)
	sort.Slice(allEntries, func(idxLeft, idxRight int) bool {
		return allEntries[idxLeft].Timestamp.After(allEntries[idxRight].Timestamp)
	})

	if len(allEntries) > maxEntries {
		allEntries = allEntries[:maxEntries]
	}

	return allEntries
}

// queryEventLog uses PowerShell to read Windows Event Log entries for a specific source
func queryEventLog(logName, sourceName string, hoursBack, maxResults int) []SystemLogEntry {
	// Use PowerShell Get-WinEvent with FilterHashtable for efficiency
	psScript := fmt.Sprintf(
		`Get-WinEvent -FilterHashtable @{LogName='%s'; ProviderName='%s'; StartTime=(Get-Date).AddHours(-%d)} -MaxEvents %d -ErrorAction SilentlyContinue | ForEach-Object { "$($_.TimeCreated.ToString('o'))|$($_.LevelDisplayName)|$($_.Message -replace '\r?\n',' ')" }`,
		logName, sourceName, hoursBack, maxResults,
	)

	cmdOutput, cmdError := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", psScript).Output()
	if cmdError != nil {
		return nil
	}

	return parseEventLogOutput(string(cmdOutput), "eventlog")
}

// queryCrashEvents searches for application crash events (WER, Application Error) mentioning our exe
func queryCrashEvents(hoursBack, maxResults int) []SystemLogEntry {
	// Search for crash events from "Application Error" and "Windows Error Reporting" sources
	psScript := fmt.Sprintf(
		`Get-WinEvent -FilterHashtable @{LogName='Application'; ProviderName='Application Error','Windows Error Reporting'; StartTime=(Get-Date).AddHours(-%d)} -MaxEvents 100 -ErrorAction SilentlyContinue | Where-Object { $_.Message -match 'VPNMultiTunnel' -or $_.Message -match 'vpnmultitunnel' } | Select-Object -First %d | ForEach-Object { "$($_.TimeCreated.ToString('o'))|$($_.LevelDisplayName)|$($_.Message -replace '\r?\n',' ')" }`,
		hoursBack, maxResults,
	)

	cmdOutput, cmdError := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", psScript).Output()
	if cmdError != nil {
		return nil
	}

	return parseEventLogOutput(string(cmdOutput), "crash")
}

// parseEventLogOutput parses the pipe-delimited output from PowerShell
func parseEventLogOutput(rawOutput string, sourceLabel string) []SystemLogEntry {
	var parsedEntries []SystemLogEntry

	scanner := bufio.NewScanner(strings.NewReader(rawOutput))
	for scanner.Scan() {
		lineText := strings.TrimSpace(scanner.Text())
		if lineText == "" {
			continue
		}

		parts := strings.SplitN(lineText, "|", 3)
		if len(parts) < 3 {
			continue
		}

		parsedTimestamp, parseError := time.Parse(time.RFC3339Nano, strings.TrimSpace(parts[0]))
		if parseError != nil {
			// Try without nanoseconds
			parsedTimestamp, parseError = time.Parse(time.RFC3339, strings.TrimSpace(parts[0]))
			if parseError != nil {
				continue
			}
		}

		eventLevel := normalizeEventLevel(strings.TrimSpace(parts[1]))
		eventMessage := strings.TrimSpace(parts[2])

		parsedEntries = append(parsedEntries, SystemLogEntry{
			Timestamp: parsedTimestamp,
			Source:    sourceLabel,
			Level:    eventLevel,
			Message:  eventMessage,
		})
	}

	return parsedEntries
}

// normalizeEventLevel converts Windows Event Log level names to our standard levels
func normalizeEventLevel(windowsLevel string) string {
	switch strings.ToLower(windowsLevel) {
	case "error", "critical":
		return "error"
	case "warning":
		return "warn"
	case "information", "info":
		return "info"
	default:
		return "info"
	}
}

// GetCombinedSystemLogs returns both service.log entries and Windows Event Log entries, merged and sorted
func GetCombinedSystemLogs(maxEntries int) []SystemLogEntry {
	serviceEntries := GetServiceLogs(maxEntries)
	eventEntries := GetWindowsEventLogs(maxEntries, 168) // 7 days

	allEntries := make([]SystemLogEntry, 0, len(serviceEntries)+len(eventEntries))
	allEntries = append(allEntries, serviceEntries...)
	allEntries = append(allEntries, eventEntries...)

	// Sort by timestamp (oldest first, matching app log convention)
	sort.Slice(allEntries, func(idxLeft, idxRight int) bool {
		return allEntries[idxLeft].Timestamp.Before(allEntries[idxRight].Timestamp)
	})

	if len(allEntries) > maxEntries {
		allEntries = allEntries[len(allEntries)-maxEntries:]
	}

	return allEntries
}
