package localaitools

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// connectInMemory wires an MCP server (built via NewServer) to a client over a
// paired in-memory transport (net.Pipe). Returns the client session and a
// teardown closure that cleans up server + client.
func connectInMemory(t *testing.T, client LocalAIClient, opts Options) (context.Context, *mcp.ClientSession, func()) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	srv := NewServer(client, opts)
	t1, t2 := mcp.NewInMemoryTransports()

	serverSession, err := srv.Connect(ctx, t1, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}

	c := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0"}, nil)
	clientSession, err := c.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}

	return ctx, clientSession, func() {
		_ = clientSession.Close()
		_ = serverSession.Wait()
		cancel()
	}
}

// listToolNames returns the sorted list of tool names exposed by the server.
func listToolNames(t *testing.T, ctx context.Context, sess *mcp.ClientSession) []string {
	t.Helper()
	res, err := sess.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	names := make([]string, 0, len(res.Tools))
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
	}
	sort.Strings(names)
	return names
}

// callTool is a small wrapper to reduce boilerplate.
// CallToolParams.Arguments is declared as `any` and the SDK marshals it for the
// wire — passing a pre-marshalled []byte (or json.RawMessage) here would be
// double-encoded as a base64 string.
func callTool(t *testing.T, ctx context.Context, sess *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	t.Helper()
	params := &mcp.CallToolParams{Name: name, Arguments: args}
	res, err := sess.CallTool(ctx, params)
	if err != nil {
		t.Fatalf("call tool %s: %v", name, err)
	}
	return res
}

// resultText concatenates all TextContent items of a result.
func resultText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// expectedFullCatalog is the tool set when DisableMutating=false. Sorted.
var expectedFullCatalog = []string{
	"delete_model",
	"edit_model_config",
	"gallery_search",
	"get_job_status",
	"get_model_config",
	"import_model_uri",
	"install_backend",
	"install_model",
	"list_backends",
	"list_galleries",
	"list_installed_models",
	"list_known_backends",
	"list_nodes",
	"reload_models",
	"system_info",
	"toggle_model_pinned",
	"toggle_model_state",
	"upgrade_backend",
	"vram_estimate",
}

// expectedReadOnlyCatalog is the tool set when DisableMutating=true. Sorted.
var expectedReadOnlyCatalog = []string{
	"gallery_search",
	"get_job_status",
	"get_model_config",
	"list_backends",
	"list_galleries",
	"list_installed_models",
	"list_known_backends",
	"list_nodes",
	"system_info",
	"vram_estimate",
}

func TestServerRegistersExpectedToolCatalog(t *testing.T) {
	ctx, sess, done := connectInMemory(t, &fakeClient{}, Options{})
	defer done()

	got := listToolNames(t, ctx, sess)
	if !sliceEqual(got, expectedFullCatalog) {
		t.Errorf("tool catalog mismatch.\n got: %v\nwant: %v", got, expectedFullCatalog)
	}
}

func TestDisableMutatingSkipsMutators(t *testing.T) {
	ctx, sess, done := connectInMemory(t, &fakeClient{}, Options{DisableMutating: true})
	defer done()

	got := listToolNames(t, ctx, sess)
	if !sliceEqual(got, expectedReadOnlyCatalog) {
		t.Errorf("read-only catalog mismatch.\n got: %v\nwant: %v", got, expectedReadOnlyCatalog)
	}
}

func TestEachToolDispatchesToClient(t *testing.T) {
	type tc struct {
		tool       string
		args       any
		wantMethod string
	}
	cases := []tc{
		{"gallery_search", GallerySearchQuery{Query: "qwen"}, "GallerySearch"},
		{"list_installed_models", map[string]any{"capability": "chat"}, "ListInstalledModels"},
		{"list_galleries", struct{}{}, "ListGalleries"},
		{"list_backends", struct{}{}, "ListBackends"},
		{"list_known_backends", struct{}{}, "ListKnownBackends"},
		{"system_info", struct{}{}, "SystemInfo"},
		{"list_nodes", struct{}{}, "ListNodes"},
		{"install_model", InstallModelRequest{ModelName: "test/foo"}, "InstallModel"},
		{"import_model_uri", ImportModelURIRequest{URI: "Qwen/Qwen3-4B-GGUF"}, "ImportModelURI"},
		{"delete_model", map[string]any{"name": "foo"}, "DeleteModel"},
		{"install_backend", InstallBackendRequest{BackendName: "llama-cpp"}, "InstallBackend"},
		{"upgrade_backend", map[string]any{"name": "llama-cpp"}, "UpgradeBackend"},
		{"edit_model_config", map[string]any{"name": "foo", "patch": map[string]any{"context_size": 4096}}, "EditModelConfig"},
		{"reload_models", struct{}{}, "ReloadModels"},
		{"toggle_model_state", map[string]any{"name": "foo", "action": "enable"}, "ToggleModelState"},
		{"toggle_model_pinned", map[string]any{"name": "foo", "action": "pin"}, "ToggleModelPinned"},
	}

	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			fc := &fakeClient{
				installModel:   func(InstallModelRequest) (string, error) { return "job-1", nil },
				installBackend: func(InstallBackendRequest) (string, error) { return "job-2", nil },
				upgradeBackend: func(string) (string, error) { return "job-3", nil },
			}
			ctx, sess, done := connectInMemory(t, fc, Options{})
			defer done()

			res := callTool(t, ctx, sess, c.tool, c.args)
			if res.IsError {
				t.Fatalf("tool %s returned error: %s", c.tool, resultText(res))
			}

			calls := fc.recorded()
			if len(calls) == 0 {
				t.Fatalf("tool %s did not call the client", c.tool)
			}
			if calls[len(calls)-1].method != c.wantMethod {
				t.Errorf("tool %s called %s, want %s", c.tool, calls[len(calls)-1].method, c.wantMethod)
			}
		})
	}
}

func TestToolErrorIsSurfacedAsToolError(t *testing.T) {
	fc := &fakeClient{
		gallerySearch: func(GallerySearchQuery) ([]GalleryModelHit, error) {
			return nil, errors.New("backend on fire")
		},
	}
	ctx, sess, done := connectInMemory(t, fc, Options{})
	defer done()

	res := callTool(t, ctx, sess, "gallery_search", GallerySearchQuery{Query: "x"})
	if !res.IsError {
		t.Fatalf("expected IsError, got success: %s", resultText(res))
	}
	if !strings.Contains(resultText(res), "backend on fire") {
		t.Errorf("error text missing original message: %s", resultText(res))
	}
}

func TestArgValidation(t *testing.T) {
	ctx, sess, done := connectInMemory(t, &fakeClient{}, Options{})
	defer done()

	cases := []struct {
		name string
		tool string
		args any
		want string
	}{
		{"install_model_missing_name", "install_model", InstallModelRequest{}, "model_name is required"},
		// Required-field misses are caught by the SDK schema validator (the
		// generated input schema marks name as required), not our handler.
		{"delete_model_missing_name", "delete_model", map[string]any{}, "missing properties"},
		{"toggle_model_state_bad_action", "toggle_model_state", map[string]any{"name": "foo", "action": "noop"}, "action must be one of"},
		{"edit_model_config_empty_patch", "edit_model_config", map[string]any{"name": "foo", "patch": map[string]any{}}, "patch is required"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := callTool(t, ctx, sess, c.tool, c.args)
			if !res.IsError {
				t.Fatalf("expected validation error for %s; got %s", c.tool, resultText(res))
			}
			if !strings.Contains(resultText(res), c.want) {
				t.Errorf("error text missing %q: %s", c.want, resultText(res))
			}
		})
	}
}

func TestHolderConcurrentSafe(t *testing.T) {
	// Smoke test: many concurrent CallTool requests against the same session.
	fc := &fakeClient{}
	ctx, sess, done := connectInMemory(t, fc, Options{})
	defer done()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			callTool(t, ctx, sess, "list_galleries", struct{}{})
		}()
	}
	wg.Wait()

	if got := len(fc.recorded()); got != 20 {
		t.Errorf("recorded %d calls, want 20", got)
	}
}

func sliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
