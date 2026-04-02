package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/ohjann/ralphplusplus/internal/worker"
)

// DaemonClient communicates with the daemon over a Unix domain socket.
type DaemonClient struct {
	socketPath string
	httpClient *http.Client
}

// Connect creates a new DaemonClient that dials the given Unix socket path
// and validates the connection by fetching the current state.
func Connect(socketPath string) (*DaemonClient, error) {
	c := &DaemonClient{
		socketPath: socketPath,
		httpClient: &http.Client{
			Transport: &http.Transport{
				DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("unix", socketPath)
				},
			},
		},
	}

	// Validate connection by fetching state
	if _, err := c.GetState(); err != nil {
		return nil, fmt.Errorf("connect to daemon: %w", err)
	}

	return c, nil
}

// StreamEvents connects to the daemon's SSE endpoint and returns a channel
// of DaemonEvents. The channel is closed when the context is cancelled or
// the connection drops. On connection errors, an error event is sent before
// the channel is closed so the caller can decide whether to retry.
func (c *DaemonClient) StreamEvents(ctx context.Context) <-chan DaemonEvent {
	ch := make(chan DaemonEvent, 64)

	go func() {
		defer close(ch)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://daemon/events", nil)
		if err != nil {
			c.sendErrorEvent(ch, fmt.Errorf("create SSE request: %w", err))
			return
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			c.sendErrorEvent(ch, fmt.Errorf("SSE connect: %w", err))
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			c.sendErrorEvent(ch, fmt.Errorf("SSE status %d", resp.StatusCode))
			return
		}

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()

			// SSE format: "data: <json>"
			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			jsonData := strings.TrimPrefix(line, "data: ")
			var evt DaemonEvent
			if err := json.Unmarshal([]byte(jsonData), &evt); err != nil {
				continue
			}

			select {
			case ch <- evt:
			case <-ctx.Done():
				return
			}
		}

		if err := scanner.Err(); err != nil && ctx.Err() == nil {
			c.sendErrorEvent(ch, fmt.Errorf("SSE read: %w", err))
		}
	}()

	return ch
}

// sendErrorEvent sends a synthetic error event on the channel.
func (c *DaemonClient) sendErrorEvent(ch chan<- DaemonEvent, err error) {
	errData, _ := json.Marshal(map[string]string{"error": err.Error()})
	select {
	case ch <- DaemonEvent{Type: "error", Data: errData}:
	default:
	}
}

// SendCommand POSTs a JSON body to the given endpoint path.
func (c *DaemonClient) SendCommand(endpoint string, body interface{}) error {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal command: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	resp, err := c.httpClient.Post("http://daemon"+endpoint, "application/json", reqBody)
	if err != nil {
		return fmt.Errorf("send command %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		return fmt.Errorf("command %s returned %d: %s", endpoint, resp.StatusCode, string(respBody))
	}

	return nil
}

// GetState fetches a full state snapshot from the daemon.
func (c *DaemonClient) GetState() (DaemonStateEvent, error) {
	resp, err := c.httpClient.Get("http://daemon/api/state")
	if err != nil {
		return DaemonStateEvent{}, fmt.Errorf("get state: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return DaemonStateEvent{}, fmt.Errorf("get state returned %d", resp.StatusCode)
	}

	var state DaemonStateEvent
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		return DaemonStateEvent{}, fmt.Errorf("decode state: %w", err)
	}

	return state, nil
}

// GetWorkerActivity fetches recent activity log lines for a specific worker.
func (c *DaemonClient) GetWorkerActivity(workerID string) (string, error) {
	resp, err := c.httpClient.Get("http://daemon/api/worker/" + workerID + "/activity")
	if err != nil {
		return "", fmt.Errorf("get worker activity: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("get worker activity returned %d", resp.StatusCode)
	}

	var result struct {
		Activity string `json:"activity"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode worker activity: %w", err)
	}

	return result.Activity, nil
}

// --- Command helpers ---

// Quit asks the daemon to shut down.
func (c *DaemonClient) Quit() error {
	return c.SendCommand("/api/quit", nil)
}

// Pause asks the daemon to pause scheduling.
func (c *DaemonClient) Pause() error {
	return c.SendCommand("/api/pause", nil)
}

// Resume asks the daemon to resume scheduling.
func (c *DaemonClient) Resume() error {
	return c.SendCommand("/api/resume", nil)
}

// SendHint sends a hint to a specific worker.
func (c *DaemonClient) SendHint(workerID worker.WorkerID, text string) error {
	return c.SendCommand("/api/hint", HintRequest{
		WorkerID: workerID,
		Text:     text,
	})
}

// SubmitTask submits an ad-hoc task to the daemon.
func (c *DaemonClient) SubmitTask(description string) error {
	return c.SendCommand("/api/task", TaskRequest{
		Description: description,
	})
}
