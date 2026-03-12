package debuglog

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	mu      sync.Mutex
	logFile *os.File
)

// Init opens the debug log file. Call once at startup.
func Init(logDir string) error {
	mu.Lock()
	defer mu.Unlock()
	if logFile != nil {
		return nil
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(logDir, "debug.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	logFile = f
	Log("debug log initialized")
	return nil
}

// Log writes a timestamped message to the debug log.
func Log(format string, args ...interface{}) {
	mu.Lock()
	defer mu.Unlock()
	if logFile == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	ts := time.Now().Format("2006-01-02 15:04:05.000")
	fmt.Fprintf(logFile, "[%s] %s\n", ts, msg)
}

// Close closes the debug log file.
func Close() {
	mu.Lock()
	defer mu.Unlock()
	if logFile != nil {
		logFile.Close()
		logFile = nil
	}
}
