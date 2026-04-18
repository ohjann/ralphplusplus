package viewer

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// unixDialTimeout bounds the time spent opening a connection to the daemon
// socket. There is deliberately no timeout on the overall round trip — SSE
// streams are expected to be long-lived, and clients cancel via r.Context().
const unixDialTimeout = 2 * time.Second

// unixRoundTrip dials sock and issues req over that connection, returning the
// response. Callers must Close the body. The request's context governs the
// lifetime of the upstream connection, so browser disconnects propagate.
//
// The function uses a fresh Transport per call (no connection pooling) so that
// each request gets its own socket — the daemon may be restarted between
// calls, and we do not want to hand back a stale cached connection.
func unixRoundTrip(sock string, req *http.Request) (*http.Response, error) {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: unixDialTimeout}
			return d.DialContext(ctx, "unix", sock)
		},
		DisableKeepAlives:     true,
		ResponseHeaderTimeout: 0,
		IdleConnTimeout:       0,
	}
	// The request URL scheme/host are irrelevant for the unix dialer but must
	// be syntactically valid so net/http can build the wire request.
	req.URL.Scheme = "http"
	req.URL.Host = "daemon"
	resp, err := tr.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("unix round trip %s: %w", sock, err)
	}
	return resp, nil
}
