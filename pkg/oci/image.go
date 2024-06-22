package oci

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/containerd/containerd/archive"
	registrytypes "github.com/docker/docker/api/types/registry"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/logs"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

// ref: https://github.com/mudler/luet/blob/master/pkg/helpers/docker/docker.go#L117
type staticAuth struct {
	auth *registrytypes.AuthConfig
}

func (s staticAuth) Authorization() (*authn.AuthConfig, error) {
	if s.auth == nil {
		return nil, nil
	}
	return &authn.AuthConfig{
		Username:      s.auth.Username,
		Password:      s.auth.Password,
		Auth:          s.auth.Auth,
		IdentityToken: s.auth.IdentityToken,
		RegistryToken: s.auth.RegistryToken,
	}, nil
}

var defaultRetryBackoff = remote.Backoff{
	Duration: 1.0 * time.Second,
	Factor:   3.0,
	Jitter:   0.1,
	Steps:    3,
}

var defaultRetryPredicate = func(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) || errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) || strings.Contains(err.Error(), "connection refused") {
		logs.Warn.Printf("retrying %v", err)
		return true
	}
	return false
}

// ExtractOCIImage will extract a given targetImage into a given targetDestination
func ExtractOCIImage(img v1.Image, targetDestination string) error {
	reader := mutate.Extract(img)

	_, err := archive.Apply(context.Background(), targetDestination, reader, archive.WithNoSameOwner())

	return err
}

func ParseImageParts(image string) (tag, repository, dstimage string) {
	tag = "latest"
	repository = "library"
	if strings.Contains(image, ":") {
		parts := strings.Split(image, ":")
		image = parts[0]
		tag = parts[1]
	}
	if strings.Contains("/", image) {
		parts := strings.Split(image, "/")
		repository = parts[0]
		image = parts[1]
	}
	dstimage = image
	return tag, repository, image
}

// GetImage if returns the proper image to pull with transport and auth
// tries local daemon first and then fallbacks into remote
// if auth is nil, it will try to use the default keychain https://github.com/google/go-containerregistry/tree/main/pkg/authn#tldr-for-consumers-of-this-package
func GetImage(targetImage, targetPlatform string, auth *registrytypes.AuthConfig, t http.RoundTripper) (v1.Image, error) {
	var platform *v1.Platform
	var image v1.Image
	var err error

	if targetPlatform != "" {
		platform, err = v1.ParsePlatform(targetPlatform)
		if err != nil {
			return image, err
		}
	} else {
		platform, err = v1.ParsePlatform(fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH))
		if err != nil {
			return image, err
		}
	}

	ref, err := name.ParseReference(targetImage)
	if err != nil {
		return image, err
	}

	if t == nil {
		t = http.DefaultTransport
	}

	tr := transport.NewRetry(t,
		transport.WithRetryBackoff(defaultRetryBackoff),
		transport.WithRetryPredicate(defaultRetryPredicate),
	)

	opts := []remote.Option{
		remote.WithTransport(tr),
		remote.WithPlatform(*platform),
	}
	if auth != nil {
		opts = append(opts, remote.WithAuth(staticAuth{auth}))
	} else {
		opts = append(opts, remote.WithAuthFromKeychain(authn.DefaultKeychain))
	}

	image, err = remote.Image(ref, opts...)

	return image, err
}

func GetOCIImageSize(targetImage, targetPlatform string, auth *registrytypes.AuthConfig, t http.RoundTripper) (int64, error) {
	var size int64
	var img v1.Image
	var err error

	img, err = GetImage(targetImage, targetPlatform, auth, t)
	if err != nil {
		return size, err
	}
	layers, _ := img.Layers()
	for _, layer := range layers {
		s, _ := layer.Size()
		size += s
	}

	return size, nil
}
