package cli

import (
	"net"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestDevBrowserSkipsOccupiedPortAndCoordinatesOrigin(t *testing.T) {
	occupied, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	start := occupied.Addr().(*net.TCPAddr).Port
	port, reservation, err := reserveDevAdminPort(strconv.Itoa(start))
	if err != nil {
		t.Fatal(err)
	}
	defer reservation.Close()
	if port <= start {
		t.Fatalf("selected occupied port: %d <= %d", port, start)
	}
	if duplicate, err := net.Listen("tcp", reservation.Addr().String()); err == nil {
		duplicate.Close()
		t.Fatal("selected port was not reserved")
	}

	t.Setenv("AGINEX_WEB_ORIGINS", "https://old.example")
	t.Setenv("AGINEX_WEB_ORIGIN", "https://legacy.example")
	t.Setenv("AGINEX_DEV_TEST_PRESERVED", "preserved")
	server := exec.Command("server")
	admin := exec.Command("pnpm", "dev:admin")
	configureDevBrowser(server, admin, port)
	wantOrigins := map[string]string{"AGINEX_WEB_ORIGINS": "https://old.example", "AGINEX_WEB_ORIGIN": "https://legacy.example"}
	for _, key := range []string{"AGINEX_WEB_ORIGINS", "AGINEX_WEB_ORIGIN"} {
		count := 0
		for _, value := range server.Environ() {
			if strings.HasPrefix(value, key+"=") {
				count++
				if value != key+"="+wantOrigins[key] {
					t.Fatalf("wrong origin: %s", value)
				}
			}
		}
		if count != 1 {
			t.Fatalf("%s has %d values", key, count)
		}
	}
	if !slices.Contains(server.Environ(), "AGINEX_DEV_TEST_PRESERVED=preserved") {
		t.Fatal("unrelated environment was lost")
	}
	if want := []string{"pnpm", "dev:admin", "--hostname", "localhost", "--port", strconv.Itoa(port)}; !slices.Equal(admin.Args, want) {
		t.Fatalf("admin arguments = %v, want %v", admin.Args, want)
	}
}

func TestDevBrowserRejectsInvalidPort(t *testing.T) {
	for _, value := range []string{"0", "-1", "65536", "invalid"} {
		t.Run(value, func(t *testing.T) {
			_, listener, err := reserveDevAdminPort(value)
			if listener != nil {
				listener.Close()
			}
			if err == nil {
				t.Fatal("accepted invalid PORT")
			}
		})
	}
}

func TestDevBrowserSkipsIPv6OccupiedPort(t *testing.T) {
	occupied, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	}
	defer occupied.Close()
	start := occupied.Addr().(*net.TCPAddr).Port
	port, reservation, err := reserveDevAdminPort(strconv.Itoa(start))
	if err != nil {
		t.Fatal(err)
	}
	defer reservation.Close()
	if port <= start {
		t.Fatalf("selected occupied IPv6 port: %d <= %d", port, start)
	}
	// A failed IPv6 probe must not retain its IPv4 reservation.
	v4, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(start)))
	if err != nil {
		t.Fatalf("IPv4 reservation leaked: %v", err)
	}
	v4.Close()
}
