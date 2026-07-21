// SPDX-License-Identifier: MIT

// Package testfixtures centralizes immutable resources shared by test suites.
package testfixtures

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"

	"github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"
)

const (
	Postgres16       = "docker.io/library/postgres@sha256:33f923b05f64ca54ac4401c01126a6b92afe839a0aa0a52bc5aeb5cc958e5f20"
	Postgres16Alpine = "docker.io/library/postgres@sha256:57c72fd2a128e416c7fcc499958864df5301e940bca0a56f58fddf30ffc07777"
	NATS2Alpine      = "docker.io/library/nats@sha256:c11af972c99ae542de8925e6a7d9c533aa1eb039660420d2074beed6089b3bf0"
)

// RequireImage fails before testcontainers can fall back to a registry pull.
func RequireImage(ctx context.Context, reference, target string) error {
	docker, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return err
	}
	defer func() { _ = docker.Close() }()
	if _, err := docker.ImageInspect(ctx, reference); err != nil {
		return fmt.Errorf("required offline test image %s is not loaded; run `make test-resources TARGET=%s`: %w", reference, target, err)
	}
	return nil
}

func DockerNetwork() (string, error) {
	name := os.Getenv("LOCALAI_TEST_DOCKER_NETWORK") //nolint:forbidigo
	if name == "" {
		return "", errors.New("offline test Docker network is not configured; run the test through scripts/run-test-offline.sh")
	}
	return name, nil
}

// ContainerEndpoint returns an address reachable from the Linux test host
// without publishing a port from the internal-only Docker network.
func ContainerEndpoint(ctx context.Context, container testcontainers.Container, port string) (string, error) {
	ip, err := container.ContainerIP(ctx)
	if err != nil {
		return "", err
	}
	if ip == "" {
		return "", errors.New("offline test container has no private network address")
	}
	return net.JoinHostPort(ip, port), nil
}
