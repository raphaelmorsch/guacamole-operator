/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package readiness

import (
	"context"
	"fmt"
	"net"
	"time"
)

// DesktopReadinessProber checks whether a desktop endpoint accepts connections.
type DesktopReadinessProber interface {
	Probe(ctx context.Context, host string, port int32) error
}

// TCPProber dials TCP to verify RDP readiness.
type TCPProber struct {
	Timeout time.Duration
}

// Probe attempts a TCP connection to host:port.
func (p TCPProber) Probe(ctx context.Context, host string, port int32) error {
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}

	dialer := net.Dialer{Timeout: timeout}
	address := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}

// FakeProber succeeds or fails based on the configured error (for unit tests).
type FakeProber struct {
	Err error
}

// Probe returns the configured error.
func (f FakeProber) Probe(_ context.Context, _ string, _ int32) error {
	return f.Err
}
