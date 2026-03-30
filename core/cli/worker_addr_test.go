package cli

import (
	"os"
	"strings"
	"testing"
)

func TestEffectiveBasePort(t *testing.T) {
	tests := []struct {
		name     string
		addr     string
		serve    string
		wantPort int
	}{
		{
			name:     "Addr takes priority",
			addr:     "worker1.example.com:60000",
			serve:    "0.0.0.0:50051",
			wantPort: 60000,
		},
		{
			name:     "Falls back to ServeAddr",
			addr:     "",
			serve:    "0.0.0.0:50051",
			wantPort: 50051,
		},
		{
			name:     "Returns 50051 when neither set",
			addr:     "",
			serve:    "",
			wantPort: 50051,
		},
		{
			name:     "Addr with custom port",
			addr:     "10.0.0.5:7000",
			serve:    "",
			wantPort: 7000,
		},
		{
			name:     "Invalid port in Addr falls through to ServeAddr",
			addr:     "host:notanumber",
			serve:    "0.0.0.0:9999",
			wantPort: 9999,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &WorkerCMD{Addr: tt.addr, ServeAddr: tt.serve}
			got := cmd.effectiveBasePort()
			if got != tt.wantPort {
				t.Errorf("effectiveBasePort() = %d, want %d", got, tt.wantPort)
			}
		})
	}
}

func TestAdvertiseAddr(t *testing.T) {
	hostname, _ := os.Hostname()

	tests := []struct {
		name      string
		advertise string
		addr      string
		serve     string
		want      string // exact match, or prefix check if empty
		wantHost  string // if non-empty, check host portion
		wantPort  string // if non-empty, check port portion
	}{
		{
			name:      "AdvertiseAddr takes priority",
			advertise: "public.example.com:50051",
			addr:      "10.0.0.5:60000",
			want:      "public.example.com:50051",
		},
		{
			name: "Returns Addr when set",
			addr: "worker1.example.com:60000",
			want: "worker1.example.com:60000",
		},
		{
			name:     "Falls back to hostname:basePort",
			addr:     "",
			serve:    "0.0.0.0:50051",
			wantPort: "50051",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &WorkerCMD{
				AdvertiseAddr: tt.advertise,
				Addr:          tt.addr,
				ServeAddr:     tt.serve,
			}
			got := cmd.advertiseAddr()
			if tt.want != "" && got != tt.want {
				t.Errorf("advertiseAddr() = %q, want %q", got, tt.want)
			}
			if tt.wantHost != "" {
				host, _, _ := strings.Cut(got, ":")
				if host != tt.wantHost {
					t.Errorf("advertiseAddr() host = %q, want %q", host, tt.wantHost)
				}
			}
			if tt.wantPort != "" {
				_, port, _ := strings.Cut(got, ":")
				if port != tt.wantPort {
					t.Errorf("advertiseAddr() port = %q, want %q", port, tt.wantPort)
				}
			}
			// When falling back, host should be hostname or localhost
			if tt.want == "" && tt.wantHost == "" {
				host, _, _ := strings.Cut(got, ":")
				if hostname != "" && host != hostname {
					t.Errorf("advertiseAddr() host = %q, want hostname %q", host, hostname)
				}
			}
		})
	}
}

func TestResolveHTTPAddr(t *testing.T) {
	tests := []struct {
		name     string
		httpAddr string
		addr     string
		serve    string
		want     string
	}{
		{
			name:     "HTTPAddr takes priority",
			httpAddr: "0.0.0.0:8080",
			want:     "0.0.0.0:8080",
		},
		{
			name:  "Derives from Addr port minus 1",
			addr:  "worker1:60000",
			serve: "0.0.0.0:50051",
			want:  "0.0.0.0:59999",
		},
		{
			name:  "Derives from ServeAddr port minus 1",
			serve: "0.0.0.0:50051",
			want:  "0.0.0.0:50050",
		},
		{
			name: "Default when nothing set",
			want: "0.0.0.0:50050",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &WorkerCMD{
				HTTPAddr:  tt.httpAddr,
				Addr:      tt.addr,
				ServeAddr: tt.serve,
			}
			got := cmd.resolveHTTPAddr()
			if got != tt.want {
				t.Errorf("resolveHTTPAddr() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAdvertiseHTTPAddr(t *testing.T) {
	tests := []struct {
		name          string
		advertiseHTTP string
		advertise     string
		addr          string
		serve         string
		want          string
	}{
		{
			name:          "AdvertiseHTTPAddr takes priority",
			advertiseHTTP: "public.example.com:8080",
			want:          "public.example.com:8080",
		},
		{
			name: "Derives from advertiseAddr host + basePort-1",
			addr: "worker1.example.com:60000",
			want: "worker1.example.com:59999",
		},
		{
			name:      "Uses AdvertiseAddr host with basePort-1",
			advertise: "public.example.com:60000",
			addr:      "10.0.0.5:60000",
			want:      "public.example.com:59999",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &WorkerCMD{
				AdvertiseHTTPAddr: tt.advertiseHTTP,
				AdvertiseAddr:     tt.advertise,
				Addr:              tt.addr,
				ServeAddr:         tt.serve,
			}
			got := cmd.advertiseHTTPAddr()
			if got != tt.want {
				t.Errorf("advertiseHTTPAddr() = %q, want %q", got, tt.want)
			}
		})
	}
}
