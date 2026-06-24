cmake_minimum_required(VERSION 3.12)
project(gocrispasr LANGUAGES C CXX)
set(CMAKE_POSITION_INDEPENDENT_CODE ON)
set(CMAKE_EXPORT_COMPILE_COMMANDS ON)

add_subdirectory(./sources/CrispASR)

add_library(gocrispasr MODULE cpp/crispasr_shim.cpp)
target_include_directories(gocrispasr PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/sources/CrispASR/include
    ${CMAKE_CURRENT_SOURCE_DIR}/sources/CrispASR/ggml/include)
# Link the same backend set as crispasr-cli (examples/cli/CMakeLists.txt) so
# the session API can dispatch to every compiled-in architecture, not just
# whisper. crispasr is the referencer; the backend static libs supply the
# per-architecture symbols; ggml is the math/runtime base.
target_link_libraries(gocrispasr PRIVATE
    crispasr-lib
    parakeet canary canary_ctc cohere granite_speech granite_nle
    voxtral voxtral4b qwen3_asr qwen3_tts orpheus chatterbox indextts
    kokoro voxcpm2_tts m2m100 t5_translate wav2vec2-ggml vibevoice f5-tts
    silero-lid pyannote-seg funasr paraformer sensevoice
    crisp_audio
    ggml)

if(CMAKE_CXX_COMPILER_ID MATCHES "GNU" AND CMAKE_CXX_COMPILER_VERSION VERSION_LESS 9.0)
    target_link_libraries(gocrispasr PRIVATE stdc++fs)
endif()

set_property(TARGET gocrispasr PROPERTY CXX_STANDARD 17)
set_target_properties(gocrispasr PROPERTIES LIBRARY_OUTPUT_DIRECTORY ${CMAKE_BINARY_DIR})
