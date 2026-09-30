// gRPC front of the gufo backend. All gufo specifics live in gufo_engine.*;
// this file maps backend.proto to and from that wrapper.
#include <pthread.h>
#include <signal.h>

#include <atomic>
#include <chrono>
#include <cstdint>
#include <cstdlib>
#include <exception>
#include <filesystem>
#include <fstream>
#include <iostream>
#include <memory>
#include <mutex>
#include <sstream>
#include <string>
#include <thread>
#include <utility>
#include <vector>

#include <grpcpp/ext/proto_server_reflection_plugin.h>
#include <grpcpp/grpcpp.h>
#include <grpcpp/server.h>
#include <grpcpp/server_builder.h>
#include <nlohmann/json.hpp>

#include "backend.grpc.pb.h"
#include "backend.pb.h"
#include "gfx_gate.h"
#include "gufo_engine.h"
#include "gufo_options.h"
#include "message_map.h"

#if defined(ENGINE_ENABLE_HIP)
#include "src/core/diagnostics/gpu_queues.h"
#endif

// Aliasing grpc::Status as Status would clash with the Status RPC.
using GStatus = ::grpc::Status;
using ::grpc::ServerContext;
using ::grpc::ServerWriter;
using ::grpc::StatusCode;

namespace gb = gufo_backend;

namespace {

std::mutex g_mu;
std::shared_ptr<gb::Engine> g_engine;  // shared so Free never pulls it from under a running request
std::string g_identity;                // ModelOptions.Model of the loaded model
std::atomic<::grpc::Server*> g_server{nullptr};

StatusCode CodeFor(gb::ErrorKind kind) {
  switch (kind) {
    case gb::ErrorKind::kInvalidArgument: return StatusCode::INVALID_ARGUMENT;
    case gb::ErrorKind::kResourceExhausted: return StatusCode::RESOURCE_EXHAUSTED;
    case gb::ErrorKind::kDeadlineExceeded: return StatusCode::DEADLINE_EXCEEDED;
    case gb::ErrorKind::kFailedPrecondition: return StatusCode::FAILED_PRECONDITION;
    default: return StatusCode::INTERNAL;
  }
}

// An exception escaping a handler terminates the process and takes every
// in-flight request with it, so each RPC body runs inside this.
template <typename Fn>
GStatus Guarded(const char* rpc, Fn&& body) {
  try {
    return body();
  } catch (const gb::EngineError& e) {
    return GStatus(CodeFor(e.kind()), e.what());
  } catch (const std::exception& e) {
    return GStatus(StatusCode::INTERNAL, std::string("gufo: ") + rpc + " failed: " + e.what());
  } catch (...) {
    return GStatus(StatusCode::INTERNAL, std::string("gufo: ") + rpc + " failed with an unknown error");
  }
}

// In distributed mode a recycled port can route a request meant for another
// model to this process; the controller sends the model it wants (#10952).
GStatus CheckIdentity(const backend::PredictOptions* request) {
  if (request == nullptr || request->modelidentity().empty()) return GStatus::OK;
  std::lock_guard<std::mutex> lock(g_mu);
  if (g_identity.empty() || g_identity == request->modelidentity()) return GStatus::OK;
  return GStatus(StatusCode::NOT_FOUND, "gufo: model identity mismatch: loaded \"" + g_identity +
                                            "\", requested \"" + request->modelidentity() + "\"");
}

std::shared_ptr<gb::Engine> CurrentEngine() {
  std::lock_guard<std::mutex> lock(g_mu);
  return g_engine;
}

std::vector<std::string> ReadKfdNodes() {
  std::vector<std::string> nodes;
  std::error_code ec;
  const std::filesystem::path root = "/sys/class/kfd/kfd/topology/nodes";
  for (const auto& entry : std::filesystem::directory_iterator(root, ec)) {
    std::ifstream in(entry.path() / "properties");
    if (!in) continue;
    std::ostringstream buf;
    buf << in.rdbuf();
    nodes.push_back(buf.str());
  }
  return nodes;
}

// A relative path in an option or a model config is resolved beside the model file.
std::string ResolveBeside(const std::string& model_file, const std::string& path) {
  if (path.empty() || path[0] == '/') return path;
  return (std::filesystem::path(model_file).parent_path() / path).string();
}

bool Truthy(const std::string& v) { return v == "true" || v == "1"; }

// LocalAI json.Marshal's the OpenAI tool_choice, so a mode arrives quoted
// ("\"required\"") and a function choice as an object; other callers send the
// bare mode. The engine expects the bare mode or the object text.
std::string NormalizeToolChoice(const std::string& raw) {
  if (raw.empty()) return raw;
  const auto doc = nlohmann::json::parse(raw, nullptr, /*allow_exceptions=*/false);
  if (doc.is_discarded()) return raw;
  if (doc.is_string()) return doc.get<std::string>();
  if (doc.is_null()) return "";
  return raw;
}

// Maps the request body common to Predict and PredictStream.
GStatus BuildRequest(const backend::PredictOptions* request, gb::GenRequest* out) {
  if (!request->grammar().empty())
    return GStatus(StatusCode::INVALID_ARGUMENT, "gufo: grammar and response_format are not supported yet");
  if (request->images_size() > gb::kMaxImages)
    return GStatus(StatusCode::INVALID_ARGUMENT,
                   "gufo: at most " + std::to_string(gb::kMaxImages) + " images per request");

  if (request->usetokenizertemplate() && request->messages_size() > 0) {
    for (const auto& m : request->messages()) {
      gb::PlainMessage pm;
      pm.role = m.role();
      pm.content = m.content();
      pm.name = m.name();
      pm.tool_call_id = m.tool_call_id();
      pm.reasoning = m.reasoning_content();
      std::string err;
      if (!m.tool_calls().empty() && !gb::ParseToolCallsJson(m.tool_calls(), &pm.tool_calls, &err))
        return GStatus(StatusCode::INVALID_ARGUMENT, "gufo: " + err);
      out->messages.push_back(std::move(pm));
    }
    // Images attach to the last user turn.
    for (auto it = out->messages.rbegin(); it != out->messages.rend() && request->images_size() > 0; ++it) {
      if (it->role != "user") continue;
      for (const auto& image : request->images()) it->images_b64.push_back(image);
      break;
    }
    std::string err;
    if (!gb::ParseToolsJson(request->tools(), &out->tools, &err))
      return GStatus(StatusCode::INVALID_ARGUMENT, "gufo: " + err);
    out->tool_choice = NormalizeToolChoice(request->toolchoice());
    // The engine reads an object choice as "required"; with nothing to call,
    // either one could only end in a failed request after a full generation.
    const std::string& choice = out->tool_choice;
    const bool forces_call = choice == "required" || (!choice.empty() && choice.front() == '{');
    if (forces_call && out->tools.empty())
      return GStatus(StatusCode::INVALID_ARGUMENT, "gufo: tool_choice requires a tool call but the request has no tools");
  } else if (!request->prompt().empty()) {
    out->raw_prompt = request->prompt();
  } else {
    return GStatus(StatusCode::INVALID_ARGUMENT, "gufo: the request has neither messages nor a prompt");
  }
  for (const auto& s : request->stopprompts()) out->stop.push_back(s);
  out->max_tokens = request->tokens() > 0 ? static_cast<std::size_t>(request->tokens()) : 0;

  // LocalAI sends zero for "not configured"; leaving those unset keeps the model's preset.
  auto& s = out->sampling;
  s.temperature = request->temperature();
  s.top_p = request->topp();
  s.top_k = request->topk();
  s.min_p = request->minp();
  s.repeat_penalty = request->penalty();
  s.frequency_penalty = request->frequencypenalty();
  s.presence_penalty = request->presencepenalty();
  s.seed = request->seed();

  const auto& md = request->metadata();
  if (auto it = md.find("enable_thinking"); it != md.end()) out->thinking = Truthy(it->second);
  if (auto it = md.find("reasoning_effort"); it != md.end()) out->reasoning_effort = it->second;
  if (!request->correlationid().empty()) out->client_id = request->correlationid();
  return GStatus::OK;
}

void FillFinal(const gb::GenResult& r, bool with_text, backend::Reply* reply) {
  // The streamed closing reply only needs a delta when there are tool calls:
  // its text already went out piece by piece.
  if (with_text || !r.tool_calls.empty()) {
    auto* delta = reply->add_chat_deltas();
    if (with_text) {
      delta->set_content(r.content);
      delta->set_reasoning_content(r.reasoning);
      reply->set_message(r.content);
    }
    for (std::size_t i = 0; i < r.tool_calls.size(); ++i) {
      const auto& call = r.tool_calls[i];
      auto* tc = delta->add_tool_calls();
      tc->set_index(static_cast<std::int32_t>(i));
      tc->set_id(call.id);
      tc->set_name(call.name);
      // Arguments go out as a JSON object string, as the OpenAI wire format carries them.
      nlohmann::json args = nlohmann::json::object();
      for (const auto& a : call.args) {
        if (a.is_string) {
          args[a.name] = a.value;
        } else {
          auto parsed = nlohmann::json::parse(a.value, nullptr, /*allow_exceptions=*/false);
          args[a.name] = parsed.is_discarded() ? nlohmann::json(a.value) : parsed;
        }
      }
      // Model output can hold invalid UTF-8; the default dump() throws on it.
      tc->set_arguments(args.dump(-1, ' ', false, nlohmann::json::error_handler_t::replace));
    }
  }
  reply->set_tokens(static_cast<std::int32_t>(r.completion_tokens));
  reply->set_prompt_tokens(static_cast<std::int32_t>(r.prompt_tokens));
  reply->set_timing_prompt_processing(r.prefill_ms);
  reply->set_timing_token_generation(r.decode_ms);
}

class GufoBackend final : public backend::Backend::Service {
 public:
  GStatus Health(ServerContext*, const backend::HealthMessage*, backend::Reply* reply) override {
    reply->set_message("OK");
    return GStatus::OK;
  }

  GStatus Status(ServerContext*, const backend::HealthMessage*, backend::StatusResponse* response) override {
    return Guarded("Status", [&] {
      response->set_state(CurrentEngine() ? backend::StatusResponse::READY : backend::StatusResponse::UNINITIALIZED);
      return GStatus::OK;
    });
  }

  GStatus Free(ServerContext*, const backend::HealthMessage*, backend::Result* result) override {
    return Guarded("Free", [&] {
      std::shared_ptr<gb::Engine> old;
      {
        std::lock_guard<std::mutex> lock(g_mu);
        old = std::move(g_engine);
        g_identity.clear();
      }
      old.reset();  // the engine is destroyed when the last in-flight request drops its reference
      result->set_success(true);
      result->set_message("gufo model freed");
      return GStatus::OK;
    });
  }

  GStatus LoadModel(ServerContext*, const backend::ModelOptions* request, backend::Result* result) override {
    auto fail = [&](const std::string& message) {
      result->set_success(false);
      result->set_message(message);
      return GStatus::OK;  // a failed load is a result, not an RPC error
    };
    try {
      return Load(request, result, fail);
    } catch (const std::exception& e) {
      return fail(std::string("gufo: the model failed to load: ") + e.what());
    } catch (...) {
      return fail("gufo: the model failed to load with an unknown error");
    }
  }

  GStatus TokenizeString(ServerContext*, const backend::PredictOptions* request,
                         backend::TokenizationResponse* response) override {
    return Guarded("TokenizeString", [&] {
      if (auto s = CheckIdentity(request); !s.ok()) return s;
      auto engine = CurrentEngine();
      if (!engine) return GStatus(StatusCode::FAILED_PRECONDITION, "gufo: no model is loaded");
      const auto tokens = engine->Tokenize(request->prompt());
      response->set_length(static_cast<std::int32_t>(tokens.size()));
      for (const auto t : tokens) response->add_tokens(static_cast<std::int32_t>(t));
      return GStatus::OK;
    });
  }

  GStatus Predict(ServerContext* context, const backend::PredictOptions* request, backend::Reply* reply) override {
    return Guarded("Predict", [&] {
      if (auto s = CheckIdentity(request); !s.ok()) return s;
      auto engine = CurrentEngine();
      if (!engine) return GStatus(StatusCode::FAILED_PRECONDITION, "gufo: no model is loaded");
      gb::GenRequest gen;
      if (auto s = BuildRequest(request, &gen); !s.ok()) return s;
      // Polled from gufo's scheduler thread; context outlives Generate, which
      // blocks until the request is terminal.
      const auto result = engine->Generate(gen, {}, [context]() { return context->IsCancelled(); });
      FillFinal(result, /*with_text=*/true, reply);
      return GStatus::OK;
    });
  }

  GStatus PredictStream(ServerContext* context, const backend::PredictOptions* request,
                        ServerWriter<backend::Reply>* writer) override {
    return Guarded("PredictStream", [&] {
      if (auto s = CheckIdentity(request); !s.ok()) return s;
      auto engine = CurrentEngine();
      if (!engine) return GStatus(StatusCode::FAILED_PRECONDITION, "gufo: no model is loaded");
      gb::GenRequest gen;
      if (auto s = BuildRequest(request, &gen); !s.ok()) return s;
      const auto result = engine->Generate(
          gen,
          [writer](const gb::Piece& piece) {
            backend::Reply reply;
            auto* delta = reply.add_chat_deltas();
            if (piece.is_reasoning) {
              delta->set_reasoning_content(piece.text);
            } else {
              delta->set_content(piece.text);
              reply.set_message(piece.text);
            }
            return writer->Write(reply);  // false when the client is gone: gufo then cancels
          },
          [context]() { return context->IsCancelled(); });
      backend::Reply closing;
      FillFinal(result, /*with_text=*/false, &closing);
      writer->Write(closing);
      return GStatus::OK;
    });
  }

 private:
  template <typename Fail>
  GStatus Load(const backend::ModelOptions* request, backend::Result* result, Fail& fail) {
    if (!std::getenv("GUFO_SKIP_GFX_CHECK") && !gb::AnyNodeIs(ReadKfdNodes(), gb::kGfx1151))
      return fail(gb::GateMessage());

    const std::vector<std::string> raw(request->options().begin(), request->options().end());
    const auto parsed = gb::ParseOptions(raw);
    if (!parsed.ok()) return fail(parsed.error);

    gb::LoadArgs args;
    args.model_path = !request->modelfile().empty() ? request->modelfile() : request->model();
    if (args.model_path.empty()) return fail("gufo: no model file was given");
    if (!std::filesystem::exists(args.model_path)) return fail("gufo: model file not found: " + args.model_path);
    args.mmproj_path = ResolveBeside(args.model_path, request->mmproj());
    args.max_context = request->contextsize() > 0 ? static_cast<std::uint32_t>(request->contextsize()) : 0;
    args.options = parsed.options;
    args.options.draft_model = ResolveBeside(args.model_path, args.options.draft_model);
    args.options.cache_disk = ResolveBeside(args.model_path, args.options.cache_disk);

    // gufo keeps a GGUF sha256 cache under $XDG_CACHE_HOME or $HOME; the backend user often has neither.
    if (!std::getenv("XDG_CACHE_HOME") && !request->modelpath().empty())
      setenv("XDG_CACHE_HOME", (std::filesystem::path(request->modelpath()) / ".cache").c_str(), 0);

    // A fresh engine per load: gufo's backend is loaded once, and a failed
    // load must leave the model already serving untouched.
    auto engine = std::make_shared<gb::Engine>();
    std::string error;
    // Some of gufo's own load errors carry no prefix; LocalAI shows this text to the user.
    if (!engine->Load(args, &error)) return fail(error.rfind("gufo", 0) == 0 ? error : "gufo: " + error);

    std::shared_ptr<gb::Engine> previous;
    {
      std::lock_guard<std::mutex> lock(g_mu);
      previous = std::move(g_engine);
      g_engine = std::move(engine);
      g_identity = request->model();
    }
    previous.reset();  // released outside the lock; in-flight requests keep it alive until they finish
    result->set_success(true);
    result->set_message("gufo model loaded");
    return GStatus::OK;
  }
};

void RunServer(const std::string& addr) {
  GufoBackend service;
  grpc::EnableDefaultHealthCheckService(true);
  grpc::reflection::InitProtoReflectionServerBuilderPlugin();
  grpc::ServerBuilder builder;
  builder.AddListeningPort(addr, grpc::InsecureServerCredentials());
  builder.RegisterService(&service);
  builder.SetMaxReceiveMessageSize(64 * 1024 * 1024);
  builder.SetMaxSendMessageSize(64 * 1024 * 1024);
  std::unique_ptr<grpc::Server> server(builder.BuildAndStart());
  if (!server) {
    std::cerr << "gufo grpc-server: failed to bind " << addr << "\n";
    std::exit(1);
  }
  g_server = server.get();
  std::cerr << "gufo grpc-server listening on " << addr << "\n";
  server->Wait();
}

// Server::Shutdown takes a mutex that Wait holds on the main thread, so it
// cannot run inside a signal handler (Abseil aborts on the self-deadlock). The
// signals are blocked in every thread and taken here, on an ordinary thread.
void WatchSignals(sigset_t set) {
  int sig = 0;
  if (sigwait(&set, &sig) != 0) return;
  std::cerr << "gufo grpc-server: signal " << sig << ", shutting down\n";
  if (auto* srv = g_server.load()) srv->Shutdown(std::chrono::system_clock::now() + std::chrono::seconds(3));
}

}  // namespace

int main(int argc, char** argv) {
  std::string addr = "127.0.0.1:50051";
  for (int i = 1; i < argc; ++i) {
    const std::string a = argv[i];
    const std::string flag = "--addr=";
    if (a.rfind(flag, 0) == 0) {
      addr = a.substr(flag.size());
    } else if (a == "--addr" && i + 1 < argc) {
      addr = argv[++i];
    } else if (a == "--help" || a == "-h") {
      std::cout << "Usage: grpc-server --addr=HOST:PORT\n";
      return 0;
    }
  }
#if defined(ENGINE_ENABLE_HIP)
  // The HIP runtime reads GPU_MAX_HW_QUEUES once, on the first dispatch, and
  // never gives a queue back, so the budget is set here as gufo's own
  // `serve llm` does. An operator value is never overridden.
  {
    namespace diag = gufo::diagnostics;
    const auto plan =
        diag::PlanQueues(diag::QueueProfile::kText, diag::QueryQueueCensus(), std::getenv("GPU_MAX_HW_QUEUES"));
    diag::ApplyQueuePlan(plan);
    std::cerr << diag::DescribeQueuePlan("llm", plan) << "\n";
    if (plan.may_exceed_budget) std::cerr << diag::DescribeQueuePressure(plan) << "\n";
  }
#endif
  // Blocked before gRPC starts its threads so they all inherit the mask and
  // only the watcher receives the signals.
  sigset_t signals;
  sigemptyset(&signals);
  sigaddset(&signals, SIGINT);
  sigaddset(&signals, SIGTERM);
  pthread_sigmask(SIG_BLOCK, &signals, nullptr);
  std::thread(WatchSignals, signals).detach();
  RunServer(addr);
  // Unload before static destruction, while gufo's scheduler threads can still be joined.
  std::shared_ptr<gb::Engine> engine;
  {
    std::lock_guard<std::mutex> lock(g_mu);
    engine = std::move(g_engine);
  }
  engine.reset();
  return 0;
}
