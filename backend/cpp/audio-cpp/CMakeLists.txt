cmake_minimum_required(VERSION 3.20)
project(audio-cpp-grpc-server LANGUAGES C CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(TARGET grpc-server)

set(AUDIO_CPP_DIR "${CMAKE_CURRENT_SOURCE_DIR}/audio.cpp"
    CACHE PATH "Path to the pinned audio.cpp checkout")
option(AUDIO_CPP_GRPC_BUILD_TESTS "Build engine-linked ctest binaries" OFF)

if(NOT EXISTS "${AUDIO_CPP_DIR}/CMakeLists.txt")
    message(FATAL_ERROR
        "AUDIO_CPP_DIR does not contain an audio.cpp checkout: ${AUDIO_CPP_DIR}. "
        "Run 'make audio.cpp' first.")
endif()

# Direct configurations only verify: source mutation belongs to make preparation.
find_package(Python3 REQUIRED COMPONENTS Interpreter)
execute_process(
    COMMAND "${Python3_EXECUTABLE}" "${CMAKE_CURRENT_SOURCE_DIR}/apply-upstream-patches.py"
            --source "${AUDIO_CPP_DIR}" --verify-only
    RESULT_VARIABLE _audio_patch_status
    OUTPUT_VARIABLE _audio_patch_output ERROR_VARIABLE _audio_patch_error)
if(NOT _audio_patch_status EQUAL 0)
    message(FATAL_ERROR "audio.cpp requires verified preparation: ${_audio_patch_error}")
endif()

if(APPLE)
    # Homebrew installs protobuf/grpc under a non-default prefix.
    if(CMAKE_HOST_SYSTEM_PROCESSOR MATCHES "arm64")
        set(HOMEBREW_DEFAULT_PREFIX "/opt/homebrew")
    else()
        set(HOMEBREW_DEFAULT_PREFIX "/usr/local")
    endif()
    link_directories("${HOMEBREW_DEFAULT_PREFIX}/lib")
    include_directories("${HOMEBREW_DEFAULT_PREFIX}/include")
endif()

find_package(Threads REQUIRED)
find_package(Protobuf CONFIG QUIET)
if(NOT Protobuf_FOUND)
    find_package(Protobuf REQUIRED)
endif()
find_package(gRPC CONFIG QUIET)
if(NOT gRPC_FOUND)
    # Reached only on distros whose grpc++ packaging ships no CMake config.
    # Ubuntu's libgrpc-dev does ship one, so this is dead code on LocalAI's own
    # build distro. Kept for the distros that do not.
    find_library(GRPCPP_LIB grpc++ REQUIRED)
    find_library(GRPCPP_REFLECTION_LIB grpc++_reflection REQUIRED)
    add_library(gRPC::grpc++ INTERFACE IMPORTED)
    set_target_properties(gRPC::grpc++ PROPERTIES
        INTERFACE_LINK_LIBRARIES "${GRPCPP_LIB}")
    add_library(gRPC::grpc++_reflection INTERFACE IMPORTED)
    set_target_properties(gRPC::grpc++_reflection PROPERTIES
        INTERFACE_LINK_LIBRARIES "${GRPCPP_REFLECTION_LIB}")
endif()

find_program(_PROTOC NAMES protoc REQUIRED)
find_program(_GRPC_CPP_PLUGIN NAMES grpc_cpp_plugin REQUIRED)

get_filename_component(HW_PROTO "${CMAKE_CURRENT_SOURCE_DIR}/../../backend.proto" ABSOLUTE)
get_filename_component(HW_PROTO_PATH "${HW_PROTO}" PATH)

set(HW_PROTO_SRCS "${CMAKE_CURRENT_BINARY_DIR}/backend.pb.cc")
set(HW_PROTO_HDRS "${CMAKE_CURRENT_BINARY_DIR}/backend.pb.h")
set(HW_GRPC_SRCS  "${CMAKE_CURRENT_BINARY_DIR}/backend.grpc.pb.cc")
set(HW_GRPC_HDRS  "${CMAKE_CURRENT_BINARY_DIR}/backend.grpc.pb.h")

add_custom_command(
    OUTPUT "${HW_PROTO_SRCS}" "${HW_PROTO_HDRS}" "${HW_GRPC_SRCS}" "${HW_GRPC_HDRS}"
    COMMAND ${_PROTOC}
    ARGS --grpc_out "${CMAKE_CURRENT_BINARY_DIR}"
         --cpp_out  "${CMAKE_CURRENT_BINARY_DIR}"
         -I "${HW_PROTO_PATH}"
         --plugin=protoc-gen-grpc="${_GRPC_CPP_PLUGIN}"
         "${HW_PROTO}"
    DEPENDS "${HW_PROTO}")

add_library(hw_grpc_proto STATIC
    ${HW_GRPC_SRCS} ${HW_GRPC_HDRS}
    ${HW_PROTO_SRCS} ${HW_PROTO_HDRS})
target_include_directories(hw_grpc_proto PUBLIC ${CMAKE_CURRENT_BINARY_DIR})
# Required on macOS: without these the Homebrew protobuf/grpc include dirs never
# reach this target and google/protobuf/runtime_version.h is not found.
target_link_libraries(hw_grpc_proto PUBLIC protobuf::libprotobuf gRPC::grpc++)

# TWO PROTOBUF RUNTIMES IN ONE BINARY, AND THE ONE THAT WON WAS THE WRONG ONE.
#
# engine_runtime links sentencepiece, whose default SPM_PROTOBUF_PROVIDER
# ("internal") builds the protobuf-lite 3.14.0 sources vendored under
# external/sentencepiece/third_party/protobuf-lite. Our generated backend.pb.cc
# is compiled against the toolchain's protobuf 3.21.12 headers and links
# libprotobuf.so 3.21.12. Both used to end up in the executable: 476
# google::protobuf:: symbols from that archive, 278 of them also defined by
# libprotobuf.so.
#
# The binding is decided at STATIC LINK time. Once ld pulls a sentencepiece
# member in for sentencepiece's own code, that member's protobuf definitions are
# in the executable and references from libhw_grpc_proto.a bind to them. Do NOT
# reach for -Wl,--exclude-libs: it flips those symbols to LOCAL in .dynsym and
# the breakage is unchanged, because no visibility flag revisits a static
# binding already made.
#
# What broke, measured rather than assumed:
# google::protobuf::internal::ParseContext::ParseMessage(MessageLite*, const char*)
# is what every generated _InternalParse calls for a SUBMESSAGE field and for
# nothing else. Bound to the 3.14 definition it fails, so a flat message parsed
# and every nested one did not: a TranscriptResult carrying segments serialized
# to correct bytes that the same process could not read back, and
# TranscriptLiveRequest, a oneof of submessages, could not have been parsed at
# all. 3.21 generated code was also running 3.14 arena, ArenaStringPtr and
# ExtensionSet code, which is an ABI mismatch rather than a missing feature, so
# "not observed to bite yet" was never a reason to leave it.
#
# "package" makes sentencepiece use the protobuf found above, which is the one
# the generated code was built against. It must be set before add_subdirectory,
# since that is when sentencepiece's own cache entry is created.
#
# WHAT THAT BUYS IS ONE PROTOBUF RUNTIME, not an executable free of protobuf
# symbols, and the difference matters to whoever checks this next. Measured with
# nm -C --defined-only on the linked grpc-server, 2515 google::protobuf::
# symbols are still DEFINED in it, and that is what should be there: they are
# generated code, sentencepiece::ModelProto's own _InternalParse and
# CheckTypeAndMergeFrom among them, which name protobuf types in their
# signatures and are compiled into every user of a .proto. Expecting zero would
# send a reader looking for a regression that is not one.
#
# The claim that decides whether the ABI mismatch above is gone is the RUNTIME
# one, and it holds: google::protobuf::internal::ParseContext::ParseMessage is
# UNDEFINED in the executable, so every generated _InternalParse resolves it to
# libprotobuf.so at load instead of to a vendored 3.14 copy. No vendored
# protobuf-lite archive is pulled in at all, and citrinet_asr, which parses a
# SentencePiece ModelProto at load, tokenizes correctly as a result.
set(SPM_PROTOBUF_PROVIDER "package" CACHE STRING
    "Make sentencepiece use the found protobuf, not its vendored 3.14 copy" FORCE)

# Upstream's global add_compile_options(-Wall -Wextra -Wpedantic -pedantic-errors)
# is a directory property of the subdirectory and does not reach our targets.
#
# EXCLUDE_FROM_ALL is load-bearing, do not drop it: upstream's default target set
# includes its CLI, server, converter and test binaries, none of which we ship.
# Without it every build would compile all of them. The targets we do name in
# target_link_libraries below are still built on demand, so nothing is lost.
add_subdirectory("${AUDIO_CPP_DIR}" "${CMAKE_CURRENT_BINARY_DIR}/audio-cpp" EXCLUDE_FROM_ALL)

add_executable(${TARGET}
    grpc-server.cpp
    music_adapter.cpp
    model_options.cpp
    capability_routing.cpp
    family_gate.cpp
    loaded_model.cpp
    audio_io.cpp
    audio_units.cpp
    transcript_assembly.cpp
    result_map.cpp
    stem_selection.cpp
    generation_request.cpp
    stream_delta.cpp
    wav_header.cpp
    inference_lane.cpp
    live_watchdog.cpp
)

# Two files carry a switch over an enum with no `default:` label, deliberately,
# so that -Wswitch reports an enumerator nobody handled. -Wswitch is only a
# warning by default, and a warning in a 600-file build log is a warning nobody
# reads, so it is promoted to an error on exactly these two translation units.
# Not project-wide: upstream's own sources are not held to this, and they are
# where the churn is.
#
# loaded_model.cpp mirrors engine::runtime::VoiceTaskKind onto its own Task enum.
# Its static_asserts catch an insertion or a reorder, but an enumerator APPENDED
# after the last one shifts no value, so no assertion can see it. What does see
# it is from_engine_task's switch over the engine enum. This is the difference
# between a build failure and a backend that silently runs the wrong task.
#
# capability_routing.cpp's unsupported_surface() switches UnsupportedRpc onto the
# row of unsupported_surfaces() that explains it. Left as a warning, a sixth
# enumerator added without a row BUILDS AND SHIPS, and its trailing
# `return surfaces[0];` then answers the new RPC with AudioEncode's codec reason:
# a confident, specific and false statement about audio.cpp, on the wire, on the
# one code path whose entire job is to be truthful about what this backend
# cannot do. Verified rather than assumed: adding a sixth enumerator and building
# the shipping target produced exit 0, a binary, and one warning. A compile-time
# check is the better trade than the runtime fallback it replaced only if it is
# fatal, so here it is fatal.
if(NOT MSVC)
    set_source_files_properties(loaded_model.cpp capability_routing.cpp
        PROPERTIES COMPILE_OPTIONS "-Werror=switch")
endif()

target_include_directories(${TARGET} PRIVATE
    "${AUDIO_CPP_DIR}/include"
    "${CMAKE_CURRENT_SOURCE_DIR}")

# The shipping binary is held to the same bar as the tests below. Upstream's own
# add_compile_options is a property of its directory and never reached this
# target, so until now "the build was clean" meant only that nothing was being
# checked.
if(NOT MSVC)
    target_compile_options(${TARGET} PRIVATE -Wall -Wextra -Wpedantic)
endif()

target_link_libraries(${TARGET} PRIVATE
    hw_grpc_proto
    engine_runtime
    ggml
    gRPC::grpc++
    gRPC::grpc++_reflection
    protobuf::libprotobuf
    Threads::Threads)

# ENGINE_ENABLE_CPU_ALL_VARIANTS builds ggml backends as shared objects that sit
# next to the binary in the package, so the binary must search its own directory.
# BUILD_WITH_INSTALL_RPATH keeps the build-tree binary at exactly "$ORIGIN".
# Upstream sets CMAKE_BUILD_WITH_INSTALL_RPATH in its own directory scope, which
# does not reach ours, so without this CMake also appends its build-tree library
# directory. That absolute build-host path would survive into the copied binary
# and let a package.sh that forgot to bundle libggml*.so still pass on the build
# machine while failing everywhere else.
set_target_properties(${TARGET} PROPERTIES
    BUILD_RPATH "$ORIGIN"
    INSTALL_RPATH "$ORIGIN"
    BUILD_WITH_INSTALL_RPATH TRUE)

if(AUDIO_CPP_GRPC_BUILD_TESTS)
    add_executable(music_adapter_ctest music_adapter_ctest.cpp music_adapter.cpp capability_routing.cpp)
    target_link_libraries(music_adapter_ctest PRIVATE hw_grpc_proto)
    add_test(NAME music_adapter COMMAND music_adapter_ctest)
    set_tests_properties(music_adapter PROPERTIES TIMEOUT 45)
    add_executable(music_yue2_vae_ctest music_yue2_vae_ctest.cpp)
    target_include_directories(music_yue2_vae_ctest PRIVATE "${AUDIO_CPP_DIR}")
    target_compile_options(music_yue2_vae_ctest PRIVATE -ffunction-sections -fdata-sections)
    if(APPLE)
        target_link_options(music_yue2_vae_ctest PRIVATE -Wl,-dead_strip)
    else()
        target_link_options(music_yue2_vae_ctest PRIVATE -Wl,--gc-sections)
    endif()
    target_link_libraries(music_yue2_vae_ctest PRIVATE engine_runtime ggml Threads::Threads)
    add_test(NAME music_yue2_vae COMMAND music_yue2_vae_ctest)
    set_tests_properties(music_yue2_vae PROPERTIES TIMEOUT 45)

    add_executable(music_yue2_nar_ctest music_yue2_nar_ctest.cpp
        "${AUDIO_CPP_DIR}/src/models/yue2/ar_runtime.cpp"
        "${AUDIO_CPP_DIR}/src/framework/modules/transformers/causal_decoder_runtime.cpp")
    target_include_directories(music_yue2_nar_ctest PRIVATE "${AUDIO_CPP_DIR}")
    target_compile_options(music_yue2_nar_ctest PRIVATE -ffunction-sections -fdata-sections)
    if(APPLE)
        target_link_options(music_yue2_nar_ctest PRIVATE -Wl,-dead_strip)
    else()
        target_link_options(music_yue2_nar_ctest PRIVATE -Wl,--gc-sections)
    endif()
    target_link_libraries(music_yue2_nar_ctest PRIVATE engine_runtime ggml Threads::Threads)
    add_test(NAME music_yue2_nar COMMAND music_yue2_nar_ctest)
    set_tests_properties(music_yue2_nar PROPERTIES TIMEOUT 45)

    add_executable(music_yue2_owned_ar_ctest music_yue2_owned_ar_ctest.cpp
        "${AUDIO_CPP_DIR}/src/framework/modules/transformers/causal_decoder_runtime.cpp")
    target_include_directories(music_yue2_owned_ar_ctest PRIVATE "${AUDIO_CPP_DIR}")
    target_compile_definitions(music_yue2_owned_ar_ctest PRIVATE ENGINE_YUE2_AR_TESTING=1)
    target_compile_options(music_yue2_owned_ar_ctest PRIVATE -ffunction-sections -fdata-sections)
    if(APPLE)
        target_link_options(music_yue2_owned_ar_ctest PRIVATE -Wl,-dead_strip)
    else()
        target_link_options(music_yue2_owned_ar_ctest PRIVATE -Wl,--gc-sections)
    endif()
    target_link_libraries(music_yue2_owned_ar_ctest PRIVATE engine_runtime ggml Threads::Threads)
    add_test(NAME music_yue2_owned_ar COMMAND music_yue2_owned_ar_ctest)
    set_tests_properties(music_yue2_owned_ar PROPERTIES TIMEOUT 45)

    add_executable(music_sheetsage_output_host_ctest music_sheetsage_output_host_ctest.cpp
        "${AUDIO_CPP_DIR}/src/framework/runtime/session.cpp"
        "${AUDIO_CPP_DIR}/src/framework/io/json.cpp")
    target_include_directories(music_sheetsage_output_host_ctest PRIVATE "${AUDIO_CPP_DIR}" "${AUDIO_CPP_DIR}/include")
    target_compile_options(music_sheetsage_output_host_ctest PRIVATE -ffunction-sections -fdata-sections)
    if(APPLE)
        target_link_options(music_sheetsage_output_host_ctest PRIVATE -Wl,-dead_strip)
    else()
        target_link_options(music_sheetsage_output_host_ctest PRIVATE -Wl,--gc-sections)
    endif()
    target_link_libraries(music_sheetsage_output_host_ctest PRIVATE ggml cjson_vendor Threads::Threads)
    add_test(NAME music_sheetsage_output_host COMMAND music_sheetsage_output_host_ctest)
    set_tests_properties(music_sheetsage_output_host PROPERTIES TIMEOUT 45)

    add_executable(music_sheetsage_token_host_ctest music_sheetsage_token_host_ctest.cpp
        "${AUDIO_CPP_DIR}/src/models/sheetsage/processing.cpp")
    target_include_directories(music_sheetsage_token_host_ctest PRIVATE "${AUDIO_CPP_DIR}" "${AUDIO_CPP_DIR}/include")
    target_compile_options(music_sheetsage_token_host_ctest PRIVATE -ffunction-sections -fdata-sections)
    if(APPLE)
        target_link_options(music_sheetsage_token_host_ctest PRIVATE -Wl,-dead_strip)
    else()
        target_link_options(music_sheetsage_token_host_ctest PRIVATE -Wl,--gc-sections)
    endif()
    target_link_libraries(music_sheetsage_token_host_ctest PRIVATE ggml Threads::Threads)
    add_test(NAME music_sheetsage_token_host COMMAND music_sheetsage_token_host_ctest)
    set_tests_properties(music_sheetsage_token_host PROPERTIES TIMEOUT 45)

    add_executable(music_sheetsage_decoder_host_ctest
        music_sheetsage_decoder_host_ctest.cpp
        "${AUDIO_CPP_DIR}/src/framework/core/backend.cpp"
        "${AUDIO_CPP_DIR}/src/framework/core/module.cpp"
        "${AUDIO_CPP_DIR}/src/framework/core/execution_context.cpp"
        "${AUDIO_CPP_DIR}/src/framework/core/host_memory.cpp")
    target_include_directories(music_sheetsage_decoder_host_ctest PRIVATE "${AUDIO_CPP_DIR}" "${AUDIO_CPP_DIR}/include")
    target_compile_options(music_sheetsage_decoder_host_ctest PRIVATE -ffunction-sections -fdata-sections)
    if(APPLE)
        target_link_options(music_sheetsage_decoder_host_ctest PRIVATE -Wl,-dead_strip)
    else()
        target_link_options(music_sheetsage_decoder_host_ctest PRIVATE -Wl,--gc-sections)
    endif()
    target_link_libraries(music_sheetsage_decoder_host_ctest PRIVATE ggml Threads::Threads ${CMAKE_DL_LIBS})
    find_package(OpenMP QUIET)
    if(OpenMP_CXX_FOUND)
        target_link_libraries(music_sheetsage_decoder_host_ctest PRIVATE OpenMP::OpenMP_CXX)
    endif()
    add_test(NAME music_sheetsage_decoder_host COMMAND music_sheetsage_decoder_host_ctest)
    set_tests_properties(music_sheetsage_decoder_host PROPERTIES TIMEOUT 45)

    add_executable(music_sheetsage_runtime_host_ctest
        music_sheetsage_runtime_host_ctest.cpp
        "${AUDIO_CPP_DIR}/src/framework/core/backend.cpp"
        "${AUDIO_CPP_DIR}/src/framework/core/module.cpp"
        "${AUDIO_CPP_DIR}/src/framework/core/execution_context.cpp"
        "${AUDIO_CPP_DIR}/src/framework/core/host_memory.cpp")
    target_include_directories(music_sheetsage_runtime_host_ctest PRIVATE "${AUDIO_CPP_DIR}" "${AUDIO_CPP_DIR}/include")
    target_compile_options(music_sheetsage_runtime_host_ctest PRIVATE -ffunction-sections -fdata-sections)
    if(APPLE)
        target_link_options(music_sheetsage_runtime_host_ctest PRIVATE -Wl,-dead_strip)
    else()
        target_link_options(music_sheetsage_runtime_host_ctest PRIVATE -Wl,--gc-sections)
    endif()
    target_link_libraries(music_sheetsage_runtime_host_ctest PRIVATE ggml Threads::Threads ${CMAKE_DL_LIBS})
    find_package(OpenMP QUIET)
    if(OpenMP_CXX_FOUND)
        target_link_libraries(music_sheetsage_runtime_host_ctest PRIVATE OpenMP::OpenMP_CXX)
    endif()
    add_test(NAME music_sheetsage_runtime_host COMMAND music_sheetsage_runtime_host_ctest)
    set_tests_properties(music_sheetsage_runtime_host PROPERTIES TIMEOUT 45)

    add_executable(music_sheetsage_window_cancellation_ctest music_sheetsage_window_cancellation_ctest.cpp)
    target_include_directories(music_sheetsage_window_cancellation_ctest PRIVATE "${AUDIO_CPP_DIR}" "${AUDIO_CPP_DIR}/include" "${AUDIO_CPP_DIR}/external/ggml/include")
    target_compile_options(music_sheetsage_window_cancellation_ctest PRIVATE -ffunction-sections -fdata-sections)
    if(APPLE)
        target_link_options(music_sheetsage_window_cancellation_ctest PRIVATE -Wl,-dead_strip)
    else()
        target_link_options(music_sheetsage_window_cancellation_ctest PRIVATE -Wl,--gc-sections)
    endif()
    target_link_libraries(music_sheetsage_window_cancellation_ctest PRIVATE Threads::Threads)
    add_test(NAME music_sheetsage_window_cancellation COMMAND music_sheetsage_window_cancellation_ctest)
    set_tests_properties(music_sheetsage_window_cancellation PROPERTIES TIMEOUT 45)

    add_executable(music_shared_mel_cancellation_ctest
        music_shared_mel_cancellation_ctest.cpp
        "${AUDIO_CPP_DIR}/src/framework/audio/dsp.cpp"
        "${AUDIO_CPP_DIR}/src/framework/audio/fft.cpp"
        "${AUDIO_CPP_DIR}/src/models/sheetsage/session.cpp"
        "${AUDIO_CPP_DIR}/src/framework/assets/resource_bundle.cpp"
        "${AUDIO_CPP_DIR}/src/framework/debug/trace.cpp")
    target_compile_definitions(music_shared_mel_cancellation_ctest PRIVATE
        ENGINE_MEL_TESTING=1 ENGINE_STFT_TESTING=1 ENGINE_FFT_TESTING=1)
    target_include_directories(music_shared_mel_cancellation_ctest PRIVATE "${AUDIO_CPP_DIR}/include")
    # The actual session helper lives with model-loading functions. Discard only
    # unused sections, rather than link an engine or duplicate frontend code.
    if(APPLE)
        target_compile_options(music_shared_mel_cancellation_ctest PRIVATE -ffunction-sections -fdata-sections)
        target_link_options(music_shared_mel_cancellation_ctest PRIVATE -Wl,-dead_strip)
    elseif(CMAKE_CXX_COMPILER_ID MATCHES "GNU|Clang")
        target_compile_options(music_shared_mel_cancellation_ctest PRIVATE -ffunction-sections -fdata-sections)
        target_link_options(music_shared_mel_cancellation_ctest PRIVATE -Wl,--gc-sections)
    endif()
    target_link_libraries(music_shared_mel_cancellation_ctest PRIVATE ggml Threads::Threads)
    find_package(OpenMP QUIET)
    if(OpenMP_CXX_FOUND)
        target_link_libraries(music_shared_mel_cancellation_ctest PRIVATE OpenMP::OpenMP_CXX)
    endif()
    add_test(NAME music_shared_mel_cancellation COMMAND music_shared_mel_cancellation_ctest)
    set_tests_properties(music_shared_mel_cancellation PROPERTIES TIMEOUT 45)

    add_executable(music_shared_stft_cancellation_ctest
        music_shared_stft_cancellation_ctest.cpp
        "${AUDIO_CPP_DIR}/src/framework/audio/dsp.cpp"
        "${AUDIO_CPP_DIR}/src/framework/audio/fft.cpp")
    target_compile_definitions(music_shared_stft_cancellation_ctest PRIVATE ENGINE_STFT_TESTING=1 ENGINE_FFT_TESTING=1)
    target_include_directories(music_shared_stft_cancellation_ctest PRIVATE "${AUDIO_CPP_DIR}/include")
    target_sources(music_shared_stft_cancellation_ctest PRIVATE "${AUDIO_CPP_DIR}/src/framework/debug/trace.cpp")
    target_link_libraries(music_shared_stft_cancellation_ctest PRIVATE ggml Threads::Threads)
    find_package(OpenMP QUIET)
    if(OpenMP_CXX_FOUND)
        target_link_libraries(music_shared_stft_cancellation_ctest PRIVATE OpenMP::OpenMP_CXX)
    endif()
    add_test(NAME music_shared_stft_cancellation COMMAND music_shared_stft_cancellation_ctest)
    set_tests_properties(music_shared_stft_cancellation PROPERTIES TIMEOUT 45)

    add_executable(music_shared_fft_cancellation_ctest
        music_shared_fft_cancellation_ctest.cpp
        "${AUDIO_CPP_DIR}/src/framework/audio/fft.cpp")
    target_compile_definitions(music_shared_fft_cancellation_ctest PRIVATE ENGINE_FFT_TESTING=1)
    target_include_directories(music_shared_fft_cancellation_ctest PRIVATE "${AUDIO_CPP_DIR}/include")
    target_link_libraries(music_shared_fft_cancellation_ctest PRIVATE ggml Threads::Threads)
    add_test(NAME music_shared_fft_cancellation COMMAND music_shared_fft_cancellation_ctest)
    set_tests_properties(music_shared_fft_cancellation PROPERTIES TIMEOUT 45)

    target_compile_definitions(engine_core PRIVATE ENGINE_CORE_CANCELLATION_TESTING=1)
    add_executable(music_core_cancellation_ctest music_core_cancellation_ctest.cpp)
    target_compile_definitions(music_core_cancellation_ctest PRIVATE ENGINE_CORE_CANCELLATION_TESTING=1)
    target_include_directories(music_core_cancellation_ctest PRIVATE "${AUDIO_CPP_DIR}/include")
    target_link_libraries(music_core_cancellation_ctest PRIVATE engine_runtime ggml Threads::Threads)
    target_compile_options(music_core_cancellation_ctest PRIVATE -Wall -Wextra -Wpedantic)
    add_test(NAME music_core_cancellation COMMAND music_core_cancellation_ctest)
    set_tests_properties(music_core_cancellation PROPERTIES TIMEOUT 60)

    enable_testing()

    # Compile the real frontend in this executable with test-only instrumentation.
    # Production engine objects have no observer; the class layout is unchanged.
    add_executable(music_sheetsage_frontend_cancellation_ctest
        music_sheetsage_frontend_cancellation_ctest.cpp
        "${AUDIO_CPP_DIR}/src/models/sheetsage/audio_frontend.cpp")
    target_compile_definitions(music_sheetsage_frontend_cancellation_ctest PRIVATE
        ENGINE_SHEETSAGE_FRONTEND_TESTING=1)
    target_include_directories(music_sheetsage_frontend_cancellation_ctest PRIVATE "${AUDIO_CPP_DIR}/include")
    target_link_libraries(music_sheetsage_frontend_cancellation_ctest PRIVATE engine_runtime ggml Threads::Threads)
    find_package(OpenMP QUIET)
    if(OpenMP_CXX_FOUND)
        target_link_libraries(music_sheetsage_frontend_cancellation_ctest PRIVATE OpenMP::OpenMP_CXX)
    endif()
    target_compile_options(music_sheetsage_frontend_cancellation_ctest PRIVATE -Wall -Wextra -Wpedantic)
    add_test(NAME music_sheetsage_frontend_cancellation COMMAND music_sheetsage_frontend_cancellation_ctest)
    set_tests_properties(music_sheetsage_frontend_cancellation PROPERTIES TIMEOUT 60)

    # Graph-only checkpoint, not complete offline/music cancellation acceptance.
    add_executable(music_sheetsage_cancellation_ctest music_sheetsage_cancellation_ctest.cpp)
    target_include_directories(music_sheetsage_cancellation_ctest PRIVATE "${AUDIO_CPP_DIR}/include")
    target_link_libraries(music_sheetsage_cancellation_ctest PRIVATE engine_runtime ggml Threads::Threads)
    target_compile_options(music_sheetsage_cancellation_ctest PRIVATE -Wall -Wextra -Wpedantic)
    add_test(NAME music_sheetsage_cancellation COMMAND music_sheetsage_cancellation_ctest)
    set_tests_properties(music_sheetsage_cancellation PROPERTIES TIMEOUT 60)

    # This bounded CPU test calls the linked CPU API, not dynamically loaded
    # all-variant modules. Shipping builds (tests OFF) are unaffected.
    option(AUDIO_CPP_GRPC_BUILD_MUSIC_CANCELLATION_TEST
        "Build the single-CPU music cancellation test" ON)
    if(AUDIO_CPP_GRPC_BUILD_MUSIC_CANCELLATION_TEST)
    if(ENGINE_ENABLE_CPU_ALL_VARIANTS OR GGML_CPU_ALL_VARIANTS)
        message(FATAL_ERROR
            "music_cancellation requires a single linked CPU backend; "
            "ENGINE_ENABLE_CPU_ALL_VARIANTS=ON is unsupported for this test. "
            "Set AUDIO_CPP_GRPC_BUILD_MUSIC_CANCELLATION_TEST=OFF to build other CTests, "
            "or AUDIO_CPP_GRPC_BUILD_TESTS=OFF for shipping builds.")
    endif()
    if(NOT TARGET ggml-cpu)
        message(FATAL_ERROR "music_cancellation requires the linked ggml-cpu target")
    endif()
    add_executable(music_cancellation_ctest
        music_cancellation_ctest.cpp music_cancellation.cpp inference_lane.cpp)
    target_link_libraries(music_cancellation_ctest PRIVATE
        engine_runtime ggml ggml-cpu Threads::Threads)
    target_compile_definitions(music_cancellation_ctest PRIVATE MUSIC_CPU_TEST_FAULTS)
    target_compile_options(music_cancellation_ctest PRIVATE -Wall -Wextra -Wpedantic)
    add_test(NAME music_cancellation COMMAND music_cancellation_ctest)
    set_tests_properties(music_cancellation PROPERTIES TIMEOUT 30)
    endif()

    add_executable(music_result_mapping_ctest
        music_result_mapping_ctest.cpp music_result_mapping.cpp)
    target_include_directories(music_result_mapping_ctest PRIVATE
        "${AUDIO_CPP_DIR}/include" "${CMAKE_CURRENT_SOURCE_DIR}")
    # TaskResult's JSON Value destructor is out of line in engine_runtime.
    target_link_libraries(music_result_mapping_ctest PRIVATE engine_runtime Threads::Threads)
    target_compile_options(music_result_mapping_ctest PRIVATE -Wall -Wextra -Wpedantic)
    add_test(NAME music_result_mapping COMMAND music_result_mapping_ctest)

    # These are the units whose tests CANNOT run under
    # backend/cpp/run-unit-tests.sh, because that script compiles each
    # *_test.cpp standalone with no protobuf and no audio.cpp include path.
    # They are named *_ctest.cpp so the script's glob does not pick them up and
    # fail every backend's suite; everything that can be stdlib-only still is,
    # and still lives in a *_test.cpp beside its unit.
    add_executable(result_map_ctest
        result_map_ctest.cpp
        result_map.cpp
        transcript_assembly.cpp
        audio_units.cpp)
    target_include_directories(result_map_ctest PRIVATE
        "${AUDIO_CPP_DIR}/include"
        "${CMAKE_CURRENT_SOURCE_DIR}")
    # TaskResult owns CustomSchemaOutput's JSON Value, whose destructor is
    # defined out of line in engine_runtime even when no model is loaded.
    target_link_libraries(result_map_ctest PRIVATE
        engine_runtime
        hw_grpc_proto
        protobuf::libprotobuf
        Threads::Threads)
    target_compile_options(result_map_ctest PRIVATE -Wall -Wextra -Wpedantic)
    add_test(NAME result_map COMMAND result_map_ctest)

    # Unlike result_map_ctest, generation_request touches only the plain
    # structs in engine/framework/runtime/session.h plus the generated protobuf
    # messages, so the headers are all it needs and no engine_runtime is linked.
    add_executable(generation_request_ctest
        generation_request_ctest.cpp
        generation_request.cpp)
    target_include_directories(generation_request_ctest PRIVATE
        "${AUDIO_CPP_DIR}/include"
        "${CMAKE_CURRENT_SOURCE_DIR}"
        # session.h reaches ggml.h through core/backend.h, and this target links
        # no ggml, so it has to name the include directory itself.
        "${AUDIO_CPP_DIR}/external/ggml/include")
    target_link_libraries(generation_request_ctest PRIVATE
        hw_grpc_proto
        protobuf::libprotobuf
        Threads::Threads)
    target_compile_options(generation_request_ctest PRIVATE -Wall -Wextra -Wpedantic)
    add_test(NAME generation_request COMMAND generation_request_ctest)

    add_executable(audio_io_ctest
        audio_io_ctest.cpp
        audio_io.cpp)
    target_include_directories(audio_io_ctest PRIVATE
        "${AUDIO_CPP_DIR}/include"
        "${CMAKE_CURRENT_SOURCE_DIR}")
    target_link_libraries(audio_io_ctest PRIVATE
        engine_runtime
        ggml
        Threads::Threads)
    target_compile_options(audio_io_ctest PRIVATE -Wall -Wextra -Wpedantic)
    set_target_properties(audio_io_ctest PROPERTIES
        BUILD_RPATH "$ORIGIN"
        INSTALL_RPATH "$ORIGIN"
        BUILD_WITH_INSTALL_RPATH TRUE)
    add_test(NAME audio_io COMMAND audio_io_ctest)

    # The streaming drivers live in loaded_model.cpp, which links the engine, so
    # this cannot be a standalone *_test.cpp. It builds no model and reads no
    # file: LoadedModel::Session is a plain struct holding a pointer to an
    # engine interface, so the drivers are exercised against fake sessions.
    add_executable(streaming_driver_ctest
        streaming_driver_ctest.cpp
        loaded_model.cpp
        capability_routing.cpp
        family_gate.cpp
        model_options.cpp
        inference_lane.cpp)
    target_include_directories(streaming_driver_ctest PRIVATE
        "${AUDIO_CPP_DIR}/include"
        "${CMAKE_CURRENT_SOURCE_DIR}")
    target_link_libraries(streaming_driver_ctest PRIVATE
        engine_runtime
        ggml
        Threads::Threads)
    target_compile_options(streaming_driver_ctest PRIVATE -Wall -Wextra -Wpedantic)
    # No "$ORIGIN" rpath override here, unlike the shipping target and unlike
    # audio_io_ctest. loaded_model.cpp reaches make_default_registry, so this
    # binary genuinely links libggml, and CMake's own build-tree rpath is what
    # finds it: the ggml shared objects land in ${CMAKE_CURRENT_BINARY_DIR}/bin
    # while the test binary sits one directory up. A build-host absolute path in
    # a test binary is harmless, since package.sh ships only grpc-server, and
    # forcing "$ORIGIN" here means ctest cannot start the binary at all.
    add_test(NAME streaming_driver COMMAND streaming_driver_ctest)

    # Asserts that the upstream ABSENCES capability_routing.cpp's refusal
    # messages rest on are still absences, by querying make_default_registry()
    # rather than by re-reading upstream. This is what makes an AUDIO_CPP_VERSION
    # bump that adds a codec task kind, an spk family or a streaming converter
    # fail the build instead of leaving a false statement on the wire.
    #
    # It links engine_runtime purely to run that query, which is why it lives
    # here rather than with the standalone *_test.cpp files, and it needs no
    # "$ORIGIN" rpath override for the same reason streaming_driver_ctest does
    # not: see the note above.
    add_executable(upstream_absence_ctest upstream_absence_ctest.cpp)
    target_include_directories(upstream_absence_ctest PRIVATE
        "${AUDIO_CPP_DIR}/include"
        "${CMAKE_CURRENT_SOURCE_DIR}")
    target_link_libraries(upstream_absence_ctest PRIVATE
        engine_runtime
        ggml
        Threads::Threads)
    target_compile_options(upstream_absence_ctest PRIVATE -Wall -Wextra -Wpedantic)
    add_test(NAME upstream_absence COMMAND upstream_absence_ctest)
endif()
