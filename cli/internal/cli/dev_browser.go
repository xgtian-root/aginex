package cli

import (
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// Reserve the port while the backend is built, then hand it to Next at startup.
func reserveDevAdminPort(value string) (int, net.Listener, error) {
	start := 3000
	if strings.TrimSpace(value) != "" {
		var err error
		start, err = strconv.Atoi(strings.TrimSpace(value))
		if err != nil || start < 1 || start > 65535 {
			return 0, nil, fmt.Errorf("PORT must be an integer between 1 and 65535")
		}
	}
	for port := start; port <= 65535; port++ {
		listener, err := reserveLocalhostPort(port)
		if err == nil {
			return port, listener, nil
		}
		if !errors.Is(err, syscall.EADDRINUSE) {
			return 0, nil, fmt.Errorf("reserve admin port %d: %w", port, err)
		}
	}
	return 0, nil, fmt.Errorf("no available admin port between %d and 65535", start)
}

type devPortReservation struct {
	net.Listener
	ipv6 net.Listener
}

func (r *devPortReservation) Close() error {
	err := r.Listener.Close()
	if r.ipv6 != nil {
		err = errors.Join(err, r.ipv6.Close())
	}
	return err
}

func reserveLocalhostPort(port int) (net.Listener, error) {
	// Reserve both families explicitly: a wildcard IPv6 listener does not
	// necessarily exclude IPv4 listeners on macOS.
	v4, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	v6, err := net.Listen("tcp6", net.JoinHostPort("::1", strconv.Itoa(port)))
	if err != nil && !errors.Is(err, syscall.EAFNOSUPPORT) && !errors.Is(err, syscall.EADDRNOTAVAIL) {
		_ = v4.Close()
		return nil, err
	}
	return &devPortReservation{Listener: v4, ipv6: v6}, nil
}

func configureDevBrowser(server, admin *exec.Cmd, port int) {
	origin := fmt.Sprintf("http://localhost:%d", port)
	environment := map[string]string{}
	for _, entry := range server.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		environment[key] = value
	}
	if strings.TrimSpace(environment["AGINEX_WEB_ORIGINS"]) == "" && strings.TrimSpace(environment["AGINEX_WEB_ORIGIN"]) == "" {
		environment["AGINEX_WEB_ORIGINS"] = origin
		environment["AGINEX_WEB_ORIGIN"] = origin
	}
	server.Env = environmentSlice(environment)
	admin.Args = append(admin.Args, "--hostname", "localhost", "--port", strconv.Itoa(port))
}
