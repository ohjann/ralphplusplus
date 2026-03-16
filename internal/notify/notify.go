package notify

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// Notifier sends push notifications via ntfy.sh and/or local terminal notifications.
type Notifier struct {
	topic     string
	serverURL string
	terminal  bool // send macOS/terminal notifications
}

// NewNotifier creates a Notifier. If serverURL is empty, defaults to "https://ntfy.sh".
func NewNotifier(topic string, serverURL string) *Notifier {
	if serverURL == "" {
		serverURL = "https://ntfy.sh"
	}
	return &Notifier{
		topic:     topic,
		serverURL: strings.TrimRight(serverURL, "/"),
		terminal:  true, // always enabled
	}
}

// Notify sends a push notification. Priority levels: 1=min, 3=default, 5=urgent.
// The send is non-blocking (fire-and-forget goroutine) and logs errors rather than failing.
func (n *Notifier) Notify(ctx context.Context, title string, message string, priority int) error {
	if n == nil {
		return nil
	}

	// Terminal/OS notification (always fires)
	if n.terminal {
		go terminalNotify(title, message)
	}

	// ntfy.sh push notification (only if topic configured)
	if n.topic != "" {
		go func() {
			url := fmt.Sprintf("%s/%s", n.serverURL, n.topic)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(message))
			if err != nil {
				log.Printf("notify: failed to create request: %v", err)
				return
			}
			req.Header.Set("Title", title)
			req.Header.Set("Priority", strconv.Itoa(priority))
			req.Header.Set("Tags", "robot")

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				log.Printf("notify: failed to send notification: %v", err)
				return
			}
			resp.Body.Close()
			if resp.StatusCode >= 400 {
				log.Printf("notify: server returned %d for %q", resp.StatusCode, title)
			}
		}()
	}

	return nil
}

// terminalNotify sends a local OS notification and terminal bell.
func terminalNotify(title, message string) {
	// Terminal bell (works in most terminals)
	fmt.Print("\a")

	if runtime.GOOS == "darwin" {
		// macOS: use osascript for native notification center
		script := fmt.Sprintf(`display notification %q with title "Ralph" subtitle %q`,
			message, title)
		_ = exec.Command("osascript", "-e", script).Run()
	}
}

// Helper methods for common notification events.

// StoryComplete sends a notification for a completed story.
func (n *Notifier) StoryComplete(ctx context.Context, storyID, title string) {
	n.Notify(ctx, "Story Complete", fmt.Sprintf("%s: %s", storyID, title), 3)
}

// StoryFailed sends a notification for a failed story.
func (n *Notifier) StoryFailed(ctx context.Context, storyID string, err string) {
	n.Notify(ctx, "Story Failed", fmt.Sprintf("%s: %s", storyID, err), 4)
}

// StoryStuck sends a notification for a stuck story.
func (n *Notifier) StoryStuck(ctx context.Context, storyID, reason string) {
	n.Notify(ctx, "Story Stuck", fmt.Sprintf("%s: %s", storyID, reason), 4)
}

// RunComplete sends a notification when the entire run finishes.
func (n *Notifier) RunComplete(ctx context.Context, completed, total int, cost float64) {
	n.Notify(ctx, "Run Complete", fmt.Sprintf("%d/%d stories, $%.2f", completed, total, cost), 5)
}

// Error sends a notification for an unexpected crash/error.
func (n *Notifier) Error(ctx context.Context, err string) {
	n.Notify(ctx, "Ralph Error", err, 5)
}
