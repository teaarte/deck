package gateway

import (
	"net"
	"net/http"
	"time"
)

// Direct is the transport of deck's own calls of a service the owner named: no proxy, a bounded dial and wait for
// the head of an answer, a body as long as it streams.
var Direct = &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
	TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 2 * time.Minute, IdleConnTimeout: time.Minute}

// Streams is the client of a long answer such as the stream of a remote MCP server; Short of a call that ends soon.
var (
	Streams = &http.Client{Transport: Direct}
	Short   = &http.Client{Transport: Direct, Timeout: time.Minute}
)
