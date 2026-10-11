// SPDX-License-Identifier: MIT
// Compile the production adapter, including the shared streaming seam. Only
// its main is excluded; no engine or gRPC symbols are stubbed out.
#define LOCALAI_DISPATCH_CORRELATION_FIXTURE
#include "grpc-server.cpp"
#include <cassert>

int main() {
    const std::string id = "01234567-89ab-4cde-8fab-0123456789ab";
    std::multimap<grpc::string_ref, grpc::string_ref> metadata;
    metadata.emplace("localai-diagnostic-id", grpc::string_ref(id.data(), id.size()));
    assert(llama_grpc::dispatch_id(metadata) == id);
    const json role = {{"choices", json::array({{{"delta", {{"role", "assistant"}}}}})}};
    const json content = {{"content", "fixture-payload-sentinel"}};
    for (bool enabled : {false, true}) {
        for (bool null_begin : {false, true}) {
            for (bool array : {false, true}) {
                for (bool fail : {false, true}) {
                    llama_grpc::DispatchDiagnostics trace(enabled, llama_grpc::dispatch_id(metadata));
                    llama_grpc::StreamPeer peer;
                    int writes = 0, attachments = 0;
                    auto build = [](const json & res, server_task_result *) {
                        backend::Reply reply;
                        reply.set_message(res.value("content", ""));
                        return reply;
                    };
                    auto attach = [&](backend::Reply &, server_task_result *) { ++attachments; };
                    auto write = [&](const backend::Reply & reply) {
                        ++writes;
                        assert(reply.message().empty() || reply.message() == "fixture-payload-sentinel");
                        return !fail;
                    };
                    if (null_begin) {
                        dispatch_stream_result(json(), nullptr, build, attach, write, peer, trace);
                        assert(writes == 0 && trace.size() == (enabled ? 1u : 0u));
                    }
                    dispatch_stream_result(array ? json::array({role, content}) : content,
                        nullptr, build, attach, write, peer, trace);
                    assert(writes == (array && !fail ? 2 : 1));
                    assert(attachments == (array && fail ? 0 : 1));
                    assert(peer.gone() == fail);
                    assert(trace.size() == (enabled ? 3u : 0u));
                    if (enabled) {
                        assert(trace.event(1).phase == llama_grpc::DispatchPhase::first_write_attempt);
                        assert(trace.event(2).outcome == (fail ? llama_grpc::DispatchOutcome::failed : llama_grpc::DispatchOutcome::ok));
                    }
                    if (!peer.gone()) {
                        const int previous_writes = writes;
                        dispatch_stream_result(content, nullptr, build, attach, write, peer, trace);
                        assert(writes == previous_writes + 1);
                        assert(trace.size() == (enabled ? 3u : 0u));
                    }
                    // This checks peer state only, not handler cancellation or
                    // server_response_reader cleanup. Those need an engine fixture.
                    peer.observe_cancelled(true);
                    assert(peer.gone());
                }
            }
        }
    }
    metadata.emplace("localai-diagnostic-id", grpc::string_ref(id.data(), id.size()));
    assert(llama_grpc::dispatch_id(metadata).empty());
    // Actual handler pre-stream error: no model, no inference, no response write.
    server_context ctx;
    BackendServiceImpl service(ctx);
    grpc::ServerContext context;
    backend::PredictOptions request;
    auto status = service.PredictStream(&context, &request, nullptr);
    assert(status.error_code() == grpc::StatusCode::FAILED_PRECONDITION);
    // Auth must still win over the no-model error, without touching the writer.
    g_grpc_auth_token = "fixture-auth-sentinel";
    grpc::ServerContext unauthenticated;
    status = service.PredictStream(&unauthenticated, &request, nullptr);
    assert(status.error_code() == grpc::StatusCode::UNAUTHENTICATED);
    g_grpc_auth_token.clear();
}
