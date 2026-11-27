// SPDX-License-Identifier: MIT
package kubeapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/httpstream"
	httpstreamspdy "k8s.io/apimachinery/pkg/util/httpstream/spdy"
	utilnet "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/remotecommand"
)

// FieldManager is the field manager of every write.
const FieldManager = "localai-kubernetes"

// Client is everything a tool may use to talk to one cluster. Each part is
// an interface, so a test gives a fake cluster.
type Client struct {
	Typed     kubernetes.Interface
	Dynamic   dynamic.Interface
	Discovery discovery.DiscoveryInterface
	Mapper    meta.RESTMapper
	Logs      LogReader
	Metrics   MetricsReader
	Exec      Execer
	Writer    Applier

	closeIdle func()
}

// LogReader streams the log of a container.
type LogReader interface {
	Logs(ctx context.Context, ns, pod string, o *corev1.PodLogOptions) (io.ReadCloser, error)
}

// Usage is the CPU and memory use of a node or a pod.
type Usage struct {
	Namespace   string `json:"namespace,omitempty"`
	Name        string `json:"name"`
	CPUMilli    int64  `json:"cpuMillicores"`
	MemoryBytes int64  `json:"memoryBytes"`
}

// ErrNoMetrics means the cluster serves no metrics.k8s.io API.
var ErrNoMetrics = errors.New("this cluster serves no metrics.k8s.io API: install metrics-server to use top")

// MetricsReader reads the metrics.k8s.io API.
type MetricsReader interface {
	Nodes(ctx context.Context) ([]Usage, error)
	Pods(ctx context.Context, ns string) ([]Usage, error)
}

// ExecResult is the outcome of one command in a container.
type ExecResult struct {
	ExitCode  int    `json:"exitCode"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	StdoutCut bool   `json:"stdoutCut,omitempty"`
	StderrCut bool   `json:"stderrCut,omitempty"`
}

// Execer runs one command in a container with no input and no terminal.
type Execer interface {
	Exec(ctx context.Context, ns, pod, container string, cmd []string) (ExecResult, error)
}

// Applier writes with server-side apply or a patch. With dryRun the API
// server runs every check and the admission chain and stores nothing.
type Applier interface {
	Apply(ctx context.Context, gvr schema.GroupVersionResource, ns string, obj *unstructured.Unstructured, dryRun bool) (*unstructured.Unstructured, error)
	Patch(ctx context.Context, gvr schema.GroupVersionResource, ns, name string, pt types.PatchType, data []byte, dryRun bool) (*unstructured.Unstructured, error)
}

// Factory builds the client of a cluster from its config.
type Factory func(cfg *rest.Config) (*Client, error)

// NewFactory returns the factory of real clients. Every connection checks
// the address it dials and refuses an address of the host or of its local
// link (see dial.go), and no client uses a proxy.
func NewFactory() Factory { return newFactory(false) }

func newFactory(allowLoopback bool) Factory {
	return func(cfg *rest.Config) (*Client, error) {
		cfg = rest.CopyConfig(cfg)
		d := dialer(allowLoopback)
		cfg.Dial = d.DialContext
		cfg.Proxy = noProxy
		// A config with its own Dial skips the transport cache of
		// client-go, so each client built from it would open its own
		// connections. The clients of a call share one HTTP client
		// instead, and the call closes its idle connections when it ends.
		hc, err := rest.HTTPClientFor(cfg)
		if err != nil {
			return nil, err
		}
		typed, err := kubernetes.NewForConfigAndClient(cfg, hc)
		if err != nil {
			return nil, err
		}
		dyn, err := dynamic.NewForConfigAndClient(cfg, hc)
		if err != nil {
			return nil, err
		}
		disc := memory.NewMemCacheClient(typed.Discovery())
		mapper := restmapper.NewShortcutExpander(restmapper.NewDeferredDiscoveryRESTMapper(disc), disc, nil)
		return &Client{
			Typed: typed, Dynamic: dyn, Discovery: disc, Mapper: mapper,
			Logs:      podLogs{typed},
			Metrics:   metrics{typed.CoreV1().RESTClient()},
			Exec:      execer{cfg: cfg, typed: typed, dialer: d, allowLoopback: allowLoopback},
			Writer:    writer{dyn},
			closeIdle: func() { utilnet.CloseIdleConnectionsFor(hc.Transport) },
		}, nil
	}
}

type podLogs struct{ c kubernetes.Interface }

func (p podLogs) Logs(ctx context.Context, ns, pod string, o *corev1.PodLogOptions) (io.ReadCloser, error) {
	return p.c.CoreV1().Pods(ns).GetLogs(pod, o).Stream(ctx)
}

// metrics reads the metrics API with the REST client, without the metrics
// module: only names and the two numbers are needed.
type metrics struct{ rc rest.Interface }

type metricsList struct {
	Items []struct {
		Metadata struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
		Usage      map[string]resource.Quantity `json:"usage"`
		Containers []struct {
			Usage map[string]resource.Quantity `json:"usage"`
		} `json:"containers"`
	} `json:"items"`
}

func (m metrics) get(ctx context.Context, path string) (metricsList, error) {
	var out metricsList
	body, err := m.rc.Get().AbsPath(path).DoRaw(ctx)
	if err != nil {
		var st interface{ Status() metav1.Status }
		if errors.As(err, &st) && st.Status().Code == http.StatusNotFound {
			return out, ErrNoMetrics
		}
		return out, err
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return out, fmt.Errorf("the metrics API answered with an unreadable body")
	}
	return out, nil
}

func usageOf(u map[string]resource.Quantity) (int64, int64) {
	cpu, mem := u[string(corev1.ResourceCPU)], u[string(corev1.ResourceMemory)]
	return cpu.MilliValue(), mem.Value()
}

func (m metrics) Nodes(ctx context.Context) ([]Usage, error) {
	l, err := m.get(ctx, "/apis/metrics.k8s.io/v1beta1/nodes")
	if err != nil {
		return nil, err
	}
	out := make([]Usage, 0, len(l.Items))
	for _, it := range l.Items {
		c, mem := usageOf(it.Usage)
		out = append(out, Usage{Name: it.Metadata.Name, CPUMilli: c, MemoryBytes: mem})
	}
	return out, nil
}

func (m metrics) Pods(ctx context.Context, ns string) ([]Usage, error) {
	path := "/apis/metrics.k8s.io/v1beta1/pods"
	if ns != "" {
		path = "/apis/metrics.k8s.io/v1beta1/namespaces/" + ns + "/pods"
	}
	l, err := m.get(ctx, path)
	if err != nil {
		return nil, err
	}
	out := make([]Usage, 0, len(l.Items))
	for _, it := range l.Items {
		u := Usage{Namespace: it.Metadata.Namespace, Name: it.Metadata.Name}
		for _, c := range it.Containers {
			cpu, mem := usageOf(c.Usage)
			u.CPUMilli += cpu
			u.MemoryBytes += mem
		}
		out = append(out, u)
	}
	return out, nil
}

type execer struct {
	cfg           *rest.Config
	typed         kubernetes.Interface
	dialer        *net.Dialer
	allowLoopback bool
}

// cappedBuffer keeps the first MaxText bytes and drops the rest, so a command
// that prints without end cannot fill the memory of the add-on.
type cappedBuffer struct {
	b   bytes.Buffer
	cut bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if room := MaxText + 4 - c.b.Len(); room > 0 {
		if len(p) > room {
			c.b.Write(p[:room])
			c.cut = true
		} else {
			c.b.Write(p)
		}
	} else if len(p) > 0 {
		c.cut = true
	}
	return len(p), nil
}

func (c *cappedBuffer) text() (string, bool) {
	s, cut := Cap(c.b.String(), MaxText)
	return s, cut || c.cut
}

func (e execer) Exec(ctx context.Context, ns, pod, container string, cmd []string) (ExecResult, error) {
	req := e.typed.CoreV1().RESTClient().Post().Resource("pods").Namespace(ns).Name(pod).SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{Container: container, Command: cmd, Stdout: true, Stderr: true}, scheme.ParameterCodec)
	// The WebSocket executor dials with its own dialer, so the address is
	// checked before it starts; the SPDY fallback dials with the checked
	// dialer.
	if err := checkResolved(ctx, e.cfg.Host, e.allowLoopback); err != nil {
		return ExecResult{}, err
	}
	ws, err := remotecommand.NewWebSocketExecutor(e.cfg, http.MethodGet, req.URL().String())
	if err != nil {
		return ExecResult{}, err
	}
	tlsConfig, err := rest.TLSConfigFor(e.cfg)
	if err != nil {
		return ExecResult{}, err
	}
	// The upgrade transport gives the SPDY round tripper the checked dialer
	// and no proxy.
	up, err := httpstreamspdy.NewRoundTripperWithConfig(httpstreamspdy.RoundTripperConfig{PingPeriod: 5 * time.Second,
		UpgradeTransport: &http.Transport{DialContext: e.dialer.DialContext, TLSClientConfig: tlsConfig}})
	if err != nil {
		return ExecResult{}, err
	}
	rt, err := rest.HTTPWrappersForConfig(e.cfg, up)
	if err != nil {
		return ExecResult{}, err
	}
	sp, err := remotecommand.NewSPDYExecutorForTransports(rt, up, http.MethodPost, req.URL())
	if err != nil {
		return ExecResult{}, err
	}
	ex, err := remotecommand.NewFallbackExecutor(ws, sp, func(err error) bool {
		return httpstream.IsUpgradeFailure(err) || httpstream.IsHTTPSProxyError(err)
	})
	if err != nil {
		return ExecResult{}, err
	}
	var out, errb cappedBuffer
	err = ex.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: &out, Stderr: &errb})
	res := ExecResult{}
	res.Stdout, res.StdoutCut = out.text()
	res.Stderr, res.StderrCut = errb.text()
	if err != nil {
		var exit interface{ ExitStatus() int }
		if errors.As(err, &exit) {
			res.ExitCode = exit.ExitStatus()
			return res, nil
		}
		return res, err
	}
	return res, nil
}

type writer struct{ dyn dynamic.Interface }

func dryRunOf(dryRun bool) []string {
	if dryRun {
		return []string{metav1.DryRunAll}
	}
	return nil
}

func (w writer) Apply(ctx context.Context, gvr schema.GroupVersionResource, ns string, obj *unstructured.Unstructured, dryRun bool) (*unstructured.Unstructured, error) {
	return w.dyn.Resource(gvr).Namespace(ns).Apply(ctx, obj.GetName(), obj,
		metav1.ApplyOptions{FieldManager: FieldManager, Force: false, DryRun: dryRunOf(dryRun)})
}

func (w writer) Patch(ctx context.Context, gvr schema.GroupVersionResource, ns, name string, pt types.PatchType, data []byte, dryRun bool) (*unstructured.Unstructured, error) {
	return w.dyn.Resource(gvr).Namespace(ns).Patch(ctx, name, pt, data,
		metav1.PatchOptions{FieldManager: FieldManager, DryRun: dryRunOf(dryRun)})
}

// Close closes the idle connections of the client. A call closes its client
// when it ends.
func (c *Client) Close() {
	if c != nil && c.closeIdle != nil {
		c.closeIdle()
	}
}
