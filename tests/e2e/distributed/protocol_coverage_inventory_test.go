// SPDX-License-Identifier: MIT

package distributed_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	nodesvc "github.com/mudler/LocalAI/core/services/nodes"
	backendgrpc "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

type protocolCoverageClass string

const (
	processConformance protocolCoverageClass = "process-level conformance"
	genericTransport   protocolCoverageClass = "representative generic transport"
	localOnly          protocolCoverageClass = "intentionally local-only"
)

type rpcShape string

const (
	unaryRPC        rpcShape = "unary"
	serverStreamRPC rpcShape = "server-streaming"
	clientStreamRPC rpcShape = "client-streaming"
	bidiStreamRPC   rpcShape = "bidirectional-streaming"
)

const backendService = "backend.Backend"

type protocolCoverage struct {
	interfaceName  string
	method         string
	rpcMethod      string
	shape          rpcShape
	classification protocolCoverageClass
	evidence       string
	staging        bool
}

func inference(method, rpc string, shape rpcShape, class protocolCoverageClass, evidence string, staging bool) protocolCoverage {
	return protocolCoverage{
		interfaceName: "InferenceBackend",
		method:        method, rpcMethod: rpc, shape: shape,
		classification: class, evidence: evidence, staging: staging,
	}
}

func control(method, rpc string, shape rpcShape, class protocolCoverageClass, evidence string, staging bool) protocolCoverage {
	return protocolCoverage{
		interfaceName: "ControlBackend",
		method:        method, rpcMethod: rpc, shape: shape,
		classification: class, evidence: evidence, staging: staging,
	}
}

const (
	chatProcess     = "binary feature matrix: /v1/chat/completions through the tunnel owner and peer frontend"
	imageProcess    = "binary feature matrix: image API through the tunnel owner and peer frontend"
	mediaProcess    = "binary feature matrix: media API through the tunnel owner and peer frontend"
	audioProcess    = "binary feature matrix: audio API through the tunnel owner and peer frontend"
	analysisProcess = "binary feature matrix: analysis API through the tunnel owner and peer frontend"
	utilityProcess  = "binary feature matrix: utility API through the tunnel owner and peer frontend"
	stagingProcess  = "binary feature matrix: authenticated external-peer relay staging case; no owner-direct protocol-only claim"
	genericUnary    = "generic unary transport represented by Backend/Predict across owner and relay tunnel paths"
	genericServer   = "generic server stream represented by Backend/PredictStream across owner and relay tunnel paths"
	genericBidi     = "generic bidirectional stream represented by Backend/AudioTransformStream through both public frontends"
)

// backendProtocolCoverage is deliberately explicit. Reflection below discovers
// the interfaces and generated service; the literals record the human decision
// about which binary case provides behavioral coverage for each method.
var backendProtocolCoverage = []protocolCoverage{
	inference("Embeddings", "Embedding", unaryRPC, processConformance, "binary feature matrix: /v1/embeddings through both frontends", false),
	inference("PredictStream", "PredictStream", serverStreamRPC, processConformance, chatProcess, true),
	inference("Predict", "Predict", unaryRPC, processConformance, chatProcess, true),
	inference("GenerateImage", "GenerateImage", unaryRPC, processConformance, imageProcess, true),
	inference("UpscaleImage", "UpscaleImage", unaryRPC, processConformance, imageProcess, true),
	inference("GenerateVideo", "GenerateVideo", unaryRPC, processConformance, mediaProcess, true),
	inference("Generate3D", "Generate3D", unaryRPC, processConformance, mediaProcess, true),
	inference("TTS", "TTS", unaryRPC, processConformance, audioProcess, true),
	inference("TTSStream", "TTSStream", serverStreamRPC, processConformance, audioProcess, true),
	inference("SoundGeneration", "SoundGeneration", unaryRPC, processConformance, audioProcess, true),
	inference("AudioTranscription", "AudioTranscription", unaryRPC, processConformance, audioProcess, true),
	inference("AudioTranscriptionStream", "AudioTranscriptionStream", serverStreamRPC, processConformance, audioProcess, true),
	inference("Detect", "Detect", unaryRPC, processConformance, analysisProcess, true),
	inference("Depth", "Depth", unaryRPC, processConformance, analysisProcess, true),
	inference("FaceVerify", "FaceVerify", unaryRPC, processConformance, analysisProcess, false),
	inference("FaceAnalyze", "FaceAnalyze", unaryRPC, processConformance, analysisProcess, false),
	inference("VoiceVerify", "VoiceVerify", unaryRPC, processConformance, analysisProcess, true),
	inference("VoiceAnalyze", "VoiceAnalyze", unaryRPC, processConformance, analysisProcess, true),
	inference("VoiceEmbed", "VoiceEmbed", unaryRPC, processConformance, analysisProcess, true),
	inference("Rerank", "Rerank", unaryRPC, processConformance, utilityProcess, false),
	inference("TokenClassify", "TokenClassify", unaryRPC, processConformance, utilityProcess, false),
	inference("Score", "Score", unaryRPC, processConformance, utilityProcess, false),
	inference("VAD", "VAD", unaryRPC, processConformance, analysisProcess, false),
	inference("Diarize", "Diarize", unaryRPC, processConformance, analysisProcess, true),
	inference("SoundDetection", "SoundDetection", unaryRPC, processConformance, audioProcess, true),
	inference("AudioEncode", "AudioEncode", unaryRPC, genericTransport, genericUnary, false),
	inference("AudioDecode", "AudioDecode", unaryRPC, genericTransport, genericUnary, false),
	inference("AudioTransform", "AudioTransform", unaryRPC, processConformance, audioProcess, true),

	control("IsBusy", "", "", localOnly, "client-side in-flight counter; it has no backend.Backend RPC", false),
	control("HealthCheck", "Health", unaryRPC, genericTransport, genericUnary, false),
	control("LoadModel", "LoadModel", unaryRPC, processConformance, stagingProcess, true),
	control("TokenizeString", "TokenizeString", unaryRPC, processConformance, utilityProcess, false),
	control("Detokenize", "Detokenize", unaryRPC, processConformance, utilityProcess, false),
	control("Status", "Status", unaryRPC, genericTransport, genericUnary, false),
	control("StoresSet", "StoresSet", unaryRPC, processConformance, utilityProcess, false),
	control("StoresDelete", "StoresDelete", unaryRPC, processConformance, utilityProcess, false),
	control("StoresGet", "StoresGet", unaryRPC, processConformance, utilityProcess, false),
	control("StoresFind", "StoresFind", unaryRPC, processConformance, utilityProcess, false),
	control("GetTokenMetrics", "GetMetrics", unaryRPC, genericTransport, genericUnary, false),
	control("AudioTransformStream", "AudioTransformStream", bidiStreamRPC, processConformance, "binary feature matrix: /audio/transformations/stream through both public frontends", false),
	control("AudioToAudioStream", "AudioToAudioStream", bidiStreamRPC, genericTransport, genericBidi, false),
	control("AudioTranscriptionLive", "AudioTranscriptionLive", bidiStreamRPC, genericTransport, genericBidi, false),
	control("Forward", "Forward", bidiStreamRPC, genericTransport, genericBidi, false),
	control("ModelMetadata", "ModelMetadata", unaryRPC, genericTransport, genericUnary, false),
	control("StartFineTune", "StartFineTune", unaryRPC, genericTransport, genericUnary, false),
	control("FineTuneProgress", "FineTuneProgress", serverStreamRPC, genericTransport, genericServer, false),
	control("StopFineTune", "StopFineTune", unaryRPC, genericTransport, genericUnary, false),
	control("ListCheckpoints", "ListCheckpoints", unaryRPC, genericTransport, genericUnary, false),
	control("ExportModel", "ExportModel", unaryRPC, processConformance, stagingProcess, true),
	control("StartQuantization", "StartQuantization", unaryRPC, processConformance, stagingProcess, true),
	control("QuantizationProgress", "QuantizationProgress", serverStreamRPC, processConformance, stagingProcess, true),
	control("StopQuantization", "StopQuantization", unaryRPC, processConformance, stagingProcess, true),
	control("Free", "Free", unaryRPC, genericTransport, genericUnary, false),
}

type protocolCoverageSurfaces struct {
	backendMethods   map[string]rpcShape
	backendInterface map[string]struct{}
	inferenceMethods map[string]struct{}
	controlMethods   map[string]struct{}
	stagingMethods   map[string]struct{}
	topologyMethods  map[string]struct{}
}

func TestBackendProtocolCoverageInventory(t *testing.T) {
	t.Parallel()
	backendInterface := interfaceMethods(reflect.TypeOf((*backendgrpc.Backend)(nil)).Elem())
	surfaces := protocolCoverageSurfaces{
		backendMethods:   generatedBackendMethods(),
		backendInterface: backendInterface,
		inferenceMethods: interfaceMethods(reflect.TypeOf((*backendgrpc.InferenceBackend)(nil)).Elem()),
		controlMethods:   interfaceMethods(reflect.TypeOf((*backendgrpc.ControlBackend)(nil)).Elem()),
		stagingMethods:   declaredFileStagingMethods(t, backendInterface),
		topologyMethods:  fileStagingTopologyMethods(t),
	}
	if errs := validateProtocolCoverage(surfaces, backendProtocolCoverage); len(errs) != 0 {
		t.Fatalf("backend protocol coverage inventory drifted:\n  - %s", strings.Join(errs, "\n  - "))
	}
}

func TestProtocolCoverageGuardRejectsNewMethod(t *testing.T) {
	errs := validateProtocolCoverage(protocolCoverageSurfaces{
		backendMethods:   map[string]rpcShape{backendService + "/Existing": unaryRPC, backendService + "/NewRPC": unaryRPC},
		backendInterface: map[string]struct{}{"Existing": {}, "NewRPC": {}},
		inferenceMethods: map[string]struct{}{"Existing": {}, "NewRPC": {}},
	}, []protocolCoverage{{interfaceName: "InferenceBackend", method: "Existing", rpcMethod: "Existing", shape: unaryRPC, classification: processConformance, evidence: "/v1/example"}})
	if got := strings.Join(errs, "\n"); !strings.Contains(got, "NewRPC") || !strings.Contains(got, "unclassified") {
		t.Fatalf("expected an actionable unclassified-method error, got %q", got)
	}
}

func TestProtocolCoverageGuardRejectsWrongStreamShapeAndStagingDrift(t *testing.T) {
	coverage := []protocolCoverage{{interfaceName: "InferenceBackend", method: "Stream", rpcMethod: "Stream", shape: serverStreamRPC, classification: genericTransport, evidence: "fixture"}}
	errs := validateProtocolCoverage(protocolCoverageSurfaces{
		backendMethods:   map[string]rpcShape{backendService + "/Stream": bidiStreamRPC},
		backendInterface: map[string]struct{}{"Stream": {}},
		inferenceMethods: map[string]struct{}{"Stream": {}},
		stagingMethods:   map[string]struct{}{"Stream": {}},
		topologyMethods:  map[string]struct{}{"Stream": {}},
	}, coverage)
	got := strings.Join(errs, "\n")
	if !strings.Contains(got, "shape") || !strings.Contains(got, "FileStagingClient") {
		t.Fatalf("expected shape and staging drift errors, got %q", got)
	}
}

func TestDeclaredFileStagingMethodsScansEntirePackage(t *testing.T) {
	dir := t.TempDir()
	for name, source := range map[string]string{
		"file_staging_client.go": "package nodes\nfunc (f *FileStagingClient) Existing() {}\n",
		"additional_staging.go":  "package nodes\nfunc (f *FileStagingClient) AddedLater() {}\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	got := declaredFileStagingMethodsInDir(t, dir, map[string]struct{}{
		"Existing":   {},
		"AddedLater": {},
	})
	if _, ok := got["AddedLater"]; !ok {
		t.Fatalf("method declared outside file_staging_client.go was not discovered: got %v", got)
	}
}

func validateProtocolCoverage(s protocolCoverageSurfaces, coverage []protocolCoverage) []string {
	var errs []string
	byMethod := make(map[string]protocolCoverage, len(coverage))
	byRPC := make(map[string]string, len(coverage))
	classifiedStaging := map[string]struct{}{}
	for _, item := range coverage {
		if previous, exists := byMethod[item.method]; exists {
			errs = append(errs, fmt.Sprintf("duplicate classification for %s (%s and %s)", item.method, previous.interfaceName, item.interfaceName))
			continue
		}
		byMethod[item.method] = item
		actualInterface := s.inferenceMethods
		if item.interfaceName == "ControlBackend" {
			actualInterface = s.controlMethods
		} else if item.interfaceName != "InferenceBackend" {
			errs = append(errs, fmt.Sprintf("%s names unknown interface %q", item.method, item.interfaceName))
		}
		if _, ok := actualInterface[item.method]; !ok {
			errs = append(errs, fmt.Sprintf("stale %s classification for missing %s method %s", item.classification, item.interfaceName, item.method))
		}
		if item.evidence == "" {
			errs = append(errs, fmt.Sprintf("%s has no concrete coverage evidence or local-only reason", item.method))
		}
		switch item.classification {
		case processConformance, genericTransport:
			if item.rpcMethod == "" {
				errs = append(errs, fmt.Sprintf("%s is remotely classified but has no %s RPC mapping", item.method, backendService))
			}
		case localOnly:
			if item.rpcMethod != "" {
				errs = append(errs, fmt.Sprintf("%s is local-only but maps to RPC %s", item.method, item.rpcMethod))
			}
		default:
			errs = append(errs, fmt.Sprintf("%s has invalid classification %q", item.method, item.classification))
		}
		if item.rpcMethod != "" {
			fullMethod := backendService + "/" + item.rpcMethod
			if previous, exists := byRPC[fullMethod]; exists {
				errs = append(errs, fmt.Sprintf("duplicate RPC mapping %s from %s and %s", fullMethod, previous, item.method))
			} else {
				byRPC[fullMethod] = item.method
			}
			if actualShape, ok := s.backendMethods[fullMethod]; !ok {
				errs = append(errs, fmt.Sprintf("stale RPC mapping %s for %s", fullMethod, item.method))
			} else if actualShape != item.shape {
				errs = append(errs, fmt.Sprintf("RPC %s shape is %s, inventory says %s", fullMethod, actualShape, item.shape))
			}
		}
		if item.staging {
			classifiedStaging[item.method] = struct{}{}
		}
	}
	for method := range s.inferenceMethods {
		if _, ok := byMethod[method]; !ok {
			errs = append(errs, fmt.Sprintf("InferenceBackend method %s is unclassified", method))
		}
	}
	for method := range s.controlMethods {
		if _, ok := byMethod[method]; !ok {
			errs = append(errs, fmt.Sprintf("ControlBackend method %s is unclassified", method))
		}
		if _, duplicate := s.inferenceMethods[method]; duplicate {
			errs = append(errs, fmt.Sprintf("method %s appears in both InferenceBackend and ControlBackend", method))
		}
	}
	for method := range s.backendInterface {
		if _, ok := byMethod[method]; !ok {
			errs = append(errs, fmt.Sprintf("Backend method %s is unclassified", method))
		}
	}
	for method := range byMethod {
		if _, ok := s.backendInterface[method]; !ok {
			errs = append(errs, fmt.Sprintf("classified method %s is absent from Backend", method))
		}
	}
	for fullMethod := range s.backendMethods {
		if _, ok := byRPC[fullMethod]; !ok {
			errs = append(errs, fmt.Sprintf("generated RPC %s is unclassified", fullMethod))
		}
	}
	compareMethodSets(&errs, "FileStagingClient override", s.stagingMethods, classifiedStaging)
	compareMethodSets(&errs, "file-staging topology", s.stagingMethods, s.topologyMethods)
	sort.Strings(errs)
	return errs
}

func compareMethodSets(errs *[]string, surface string, want, got map[string]struct{}) {
	for method := range want {
		if _, ok := got[method]; !ok {
			*errs = append(*errs, fmt.Sprintf("%s method %s is unclassified", surface, method))
		}
	}
	for method := range got {
		if _, ok := want[method]; !ok {
			*errs = append(*errs, fmt.Sprintf("stale %s classification for %s", surface, method))
		}
	}
}

func interfaceMethods(interfaceType reflect.Type) map[string]struct{} {
	methods := make(map[string]struct{}, interfaceType.NumMethod())
	for i := 0; i < interfaceType.NumMethod(); i++ {
		methods[interfaceType.Method(i).Name] = struct{}{}
	}
	return methods
}

func generatedBackendMethods() map[string]rpcShape {
	methods := make(map[string]rpcShape, len(pb.Backend_ServiceDesc.Methods)+len(pb.Backend_ServiceDesc.Streams))
	for _, method := range pb.Backend_ServiceDesc.Methods {
		methods[pb.Backend_ServiceDesc.ServiceName+"/"+method.MethodName] = unaryRPC
	}
	for _, stream := range pb.Backend_ServiceDesc.Streams {
		shape := serverStreamRPC
		switch {
		case stream.ClientStreams && stream.ServerStreams:
			shape = bidiStreamRPC
		case stream.ClientStreams:
			shape = clientStreamRPC
		}
		methods[pb.Backend_ServiceDesc.ServiceName+"/"+stream.StreamName] = shape
	}
	return methods
}

func declaredFileStagingMethods(t *testing.T, backendMethods map[string]struct{}) map[string]struct{} {
	t.Helper()
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory while locating nodes package: %v", err)
	}
	moduleRoot, err := findModuleRoot(workingDir)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(moduleRoot, "core", "services", "nodes")
	return declaredFileStagingMethodsInDir(t, dir, backendMethods)
}

func findModuleRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("resolve module search path %q: %w", start, err)
	}
	for {
		info, statErr := os.Stat(filepath.Join(dir, "go.mod"))
		if statErr == nil && !info.IsDir() {
			return dir, nil
		}
		if statErr != nil && !os.IsNotExist(statErr) {
			return "", fmt.Errorf("inspect module marker in %s: %w", dir, statErr)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("locate LocalAI module root from %s: no go.mod found in it or any parent", start)
		}
		dir = parent
	}
}

func declaredFileStagingMethodsInDir(t *testing.T, dir string, backendMethods map[string]struct{}) map[string]struct{} {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read nodes package for FileStagingClient overrides: %v", err)
	}
	methods := map[string]struct{}{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s while discovering FileStagingClient overrides: %v", path, err)
		}
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || !fn.Name.IsExported() || len(fn.Recv.List) != 1 {
				continue
			}
			receiver := fn.Recv.List[0].Type
			if pointer, ok := receiver.(*ast.StarExpr); ok {
				receiver = pointer.X
			}
			name, ok := receiver.(*ast.Ident)
			_, backendMethod := backendMethods[fn.Name.Name]
			if ok && name.Name == reflect.TypeOf(nodesvc.FileStagingClient{}).Name() && backendMethod {
				methods[fn.Name.Name] = struct{}{}
			}
		}
	}
	return methods
}

func fileStagingTopologyMethods(t *testing.T) map[string]struct{} {
	t.Helper()
	methods := make(map[string]struct{}, len(fileStagingTopologyCoverage))
	for _, item := range fileStagingTopologyCoverage {
		if _, exists := methods[item.method]; exists {
			t.Fatalf("duplicate file-staging topology classification for %s", item.method)
		}
		if item.ownerPublicPath == "" {
			t.Fatalf("file-staging topology method %s has no owner public route", item.method)
		}
		if want := "Backend/" + item.method; item.relayProtocolPath != want {
			t.Fatalf("file-staging topology method %s relay path is %q, want %q", item.method, item.relayProtocolPath, want)
		}
		methods[item.method] = struct{}{}
	}
	return methods
}
